// Package estoque implementa o microsserviço de gerenciamento de estoque.
//
// Consome pedido.criado e pedido.excluido; publica pedido.estoque_ok ou
// estoque.indisponivel. Consome também promocao.criada, para vender o produto
// pelo preço promocional até a validade. Guarda produtos, reservas e
// promoções num banco SQLite e expõe a consulta de produtos por HTTP.
package estoque

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"ecommerce/internal/evento"
)

// Publicador é o pedaço da mensageria que o Estoque usa.
type Publicador interface {
	PublicarECommerce(ctx context.Context, routingKey string, dados any) error
}

// Servico aplica as regras de reserva e devolução sobre o saldo guardado no
// repositório.
type Servico struct {
	repositorio *Repositorio
	publicador  Publicador
}

// NovoServico liga o serviço ao banco e ao broker.
func NovoServico(repositorio *Repositorio, publicador Publicador) *Servico {
	s := &Servico{repositorio: repositorio, publicador: publicador}
	log.Printf("estoque atual: %s", s.Resumo())
	return s
}

// Tratar é o ponto de entrada do consumidor: despacha pela routing key.
func (s *Servico) Tratar(ctx context.Context, env evento.Envelope) error {
	switch env.Tipo {
	case evento.PedidoCriado:
		return s.aoPedidoCriado(ctx, env)
	case evento.PedidoExcluido:
		return s.aoPedidoExcluido(ctx, env)
	case evento.PromocaoCriada:
		return s.aoPromocaoCriada(ctx, env)
	default:
		log.Printf("evento %s ignorado: não é assunto do Estoque", env.Tipo)
		return nil
	}
}

// ProdutosDisponiveis lista os produtos com saldo positivo.
func (s *Servico) ProdutosDisponiveis(ctx context.Context) ([]Produto, error) {
	return s.repositorio.Disponiveis(ctx)
}

// Produto devolve um produto pelo ID, com qualquer saldo. O segundo retorno é
// falso quando o produto não existe.
func (s *Servico) Produto(ctx context.Context, id string) (Produto, bool, error) {
	return s.repositorio.Buscar(ctx, id)
}

// aoPedidoCriado verifica a disponibilidade dos produtos do pedido. Dando
// tudo certo, baixa o estoque e publica pedido.estoque_ok. Faltando algum
// item, não mexe em nada e publica estoque.indisponivel.
func (s *Servico) aoPedidoCriado(ctx context.Context, env evento.Envelope) error {
	var pedido evento.DadosPedidoCriado
	if err := env.DecodificarDados(&pedido); err != nil {
		return err
	}

	falta, err := s.reservar(ctx, pedido)
	if err != nil {
		return err
	}

	if falta != nil {
		log.Printf("pedido %s recusado: %s", pedido.PedidoID, falta.Motivo)
		return s.publicador.PublicarECommerce(ctx, evento.EstoqueIndisponivel, *falta)
	}

	log.Printf("pedido %s reservado. Estoque agora: %s", pedido.PedidoID, s.Resumo())

	return s.publicador.PublicarECommerce(ctx, evento.PedidoEstoqueOK, evento.DadosPedidoEstoqueOK{
		PedidoID: pedido.PedidoID,
		Itens:    pedido.Itens,
		Total:    pedido.Total,
	})
}

// reservar confere e baixa o estoque do pedido numa única transação.
//
// Devolve nil quando reservou ou quando o pedido já estava reservado.
// Devolve o motivo preenchido quando não deu, e nesse caso o estoque fica
// exatamente como estava.
func (s *Servico) reservar(ctx context.Context, pedido evento.DadosPedidoCriado) (*evento.DadosEstoqueIndisponivel, error) {
	tx, err := s.repositorio.Iniciar(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Desfazer()

	jaReservado, err := tx.Reservado(pedido.PedidoID)
	if err != nil {
		return nil, err
	}
	if jaReservado {
		log.Printf("pedido %s já estava reservado, reentrega ignorada", pedido.PedidoID)
		return nil, nil
	}

	if len(pedido.Itens) == 0 {
		return &evento.DadosEstoqueIndisponivel{
			PedidoID: pedido.PedidoID,
			Motivo:   "pedido sem itens",
		}, nil
	}

	necessario := make(map[string]int, len(pedido.Itens))
	for _, item := range pedido.Itens {
		if item.Quantidade <= 0 {
			produto, _, err := tx.Produto(item.ProdutoID)
			if err != nil {
				return nil, err
			}
			return &evento.DadosEstoqueIndisponivel{
				PedidoID:   pedido.PedidoID,
				ProdutoID:  item.ProdutoID,
				Nome:       item.Nome,
				Solicitado: item.Quantidade,
				Disponivel: produto.Quantidade,
				Motivo:     fmt.Sprintf("quantidade inválida para %s", item.ProdutoID),
			}, nil
		}
		necessario[item.ProdutoID] += item.Quantidade
	}

	for _, produtoID := range idsOrdenados(necessario) {
		quantidade := necessario[produtoID]

		produto, existe, err := tx.Produto(produtoID)
		if err != nil {
			return nil, err
		}

		if !existe {
			return &evento.DadosEstoqueIndisponivel{
				PedidoID:   pedido.PedidoID,
				ProdutoID:  produtoID,
				Solicitado: quantidade,
				Disponivel: 0,
				Motivo:     fmt.Sprintf("produto %s não existe no catálogo", produtoID),
			}, nil
		}

		if produto.Quantidade < quantidade {
			return &evento.DadosEstoqueIndisponivel{
				PedidoID:   pedido.PedidoID,
				ProdutoID:  produtoID,
				Nome:       produto.Nome,
				Solicitado: quantidade,
				Disponivel: produto.Quantidade,
				Motivo: fmt.Sprintf("estoque insuficiente de %s: pedidas %d, disponíveis %d",
					produto.Nome, quantidade, produto.Quantidade),
			}, nil
		}
	}

	for produtoID, quantidade := range necessario {
		if err := tx.Reservar(pedido.PedidoID, produtoID, quantidade); err != nil {
			return nil, err
		}
	}

	return nil, tx.Confirmar()
}

// aoPedidoExcluido devolve ao estoque o que havia sido reservado para o
// pedido. Sem reserva, não altera nada.
func (s *Servico) aoPedidoExcluido(ctx context.Context, env evento.Envelope) error {
	var excluido evento.DadosPedidoExcluido
	if err := env.DecodificarDados(&excluido); err != nil {
		return err
	}

	devolvidos, err := s.devolver(ctx, excluido.PedidoID)
	if err != nil {
		return err
	}

	if devolvidos == 0 {
		log.Printf("pedido %s excluído (%s), mas não havia reserva: nada a devolver",
			excluido.PedidoID, excluido.Motivo)
		return nil
	}

	log.Printf("pedido %s excluído (%s): %d item(ns) devolvidos. Estoque agora: %s",
		excluido.PedidoID, excluido.Motivo, devolvidos, s.Resumo())
	return nil
}

// aoPromocaoCriada guarda o preço promocional do produto. Até a validade,
// as consultas de produto, e com elas os pedidos montados pelo Gateway,
// passam a usar esse preço.
func (s *Servico) aoPromocaoCriada(ctx context.Context, env evento.Envelope) error {
	var promocao evento.DadosPromocaoCriada
	if err := env.DecodificarDados(&promocao); err != nil {
		return err
	}
	if promocao.PrecoPromocional <= 0 {
		return fmt.Errorf("promoção de %s com preço inválido: %.2f", promocao.ProdutoID, promocao.PrecoPromocional)
	}

	produto, existe, err := s.repositorio.Buscar(ctx, promocao.ProdutoID)
	switch {
	case err != nil:
		return err
	case !existe:
		return fmt.Errorf("promoção para o produto %s, que não existe no estoque", promocao.ProdutoID)
	}

	if err := s.repositorio.RegistrarPromocao(ctx, promocao.ProdutoID, promocao.PrecoPromocional, promocao.ValidaAte); err != nil {
		return err
	}

	log.Printf("promoção de %s registrada: R$ %.2f por R$ %.2f até %s",
		produto.Nome, valorDeTabela(produto), promocao.PrecoPromocional, promocao.ValidaAte.Format("02/01 15:04"))
	return nil
}

// valorDeTabela devolve o preço do produto sem promoção.
func valorDeTabela(p Produto) float64 {
	if p.PrecoOriginal > 0 {
		return p.PrecoOriginal
	}
	return p.Preco
}

// devolver repõe a reserva do pedido numa transação e diz quantos produtos
// voltaram ao saldo.
func (s *Servico) devolver(ctx context.Context, pedidoID string) (int, error) {
	tx, err := s.repositorio.Iniciar(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Desfazer()

	devolvidos, err := tx.Devolver(pedidoID)
	if err != nil {
		return 0, err
	}
	return devolvidos, tx.Confirmar()
}

// Resumo devolve o saldo atual em uma linha, para o log.
func (s *Servico) Resumo() string {
	produtos, err := s.repositorio.Todos(context.Background())
	if err != nil {
		return fmt.Sprintf("indisponível (%v)", err)
	}

	partes := make([]string, 0, len(produtos))
	for _, p := range produtos {
		partes = append(partes, fmt.Sprintf("%s=%d", p.ID, p.Quantidade))
	}
	return strings.Join(partes, " ")
}

func idsOrdenados(m map[string]int) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
