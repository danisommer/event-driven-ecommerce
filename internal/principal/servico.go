package principal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"slices"
	"strings"
	"sync"

	"ecommerce/internal/catalogo"
	"ecommerce/internal/evento"
)

// Estoque é o pedaço da API do MS Estoque que o Principal consulta.
type Estoque interface {
	Disponiveis(ctx context.Context) ([]Produto, error)
	Produto(ctx context.Context, id string) (Produto, bool, error)
}

// Erros das operações pedidas pelo frontend, que a API traduz em status HTTP.
var (
	ErrPedidoInvalido      = errors.New("pedido inválido")
	ErrInteresseInvalido   = errors.New("interesse inválido")
	ErrEstoqueIndisponivel = errors.New("o Estoque não respondeu")
	ErrBrokerIndisponivel  = errors.New("não consegui publicar no broker")
)

// ItemSolicitado é um produto e a quantidade pedidos pelo frontend.
type ItemSolicitado struct {
	ProdutoID  string `json:"produto_id"`
	Quantidade int    `json:"quantidade"`
}

// Servico junta as regras do Gateway: monta pedidos com os preços do Estoque,
// reage aos eventos que mudam o status e publica os eventos das ações do
// usuário. O mutex faz cada mudança de status, venha ela de um evento ou de um
// cancelamento pedido pelo usuário, acontecer inteira, uma de cada vez.
type Servico struct {
	mu sync.Mutex

	registro   *Registro
	estoque    Estoque
	publicador Publicador
	avisar     func(Pedido)
}

// NovoServico monta o serviço. avisar recebe cada pedido que mudou, para o
// SSE repassar ao frontend.
func NovoServico(registro *Registro, estoque Estoque, publicador Publicador, avisar func(Pedido)) *Servico {
	if avisar == nil {
		avisar = func(Pedido) {}
	}
	return &Servico{registro: registro, estoque: estoque, publicador: publicador, avisar: avisar}
}

// Tratar é o ponto de entrada do consumidor: despacha pela routing key.
func (s *Servico) Tratar(ctx context.Context, env evento.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch env.Tipo {
	case evento.PedidoEstoqueOK:
		return s.aoEstoqueOK(env)
	case evento.EstoqueIndisponivel:
		return s.aoEstoqueIndisponivel(ctx, env)
	case evento.PagamentoPendente:
		return s.aoPagamentoPendente(env)
	case evento.PagamentoAprovado:
		return s.aoPagamentoAprovado(env)
	case evento.PagamentoRecusado:
		return s.aoPagamentoRecusado(ctx, env)
	case evento.PedidoEnviado:
		return s.aoPedidoEnviado(env)
	default:
		return nil
	}
}

func (s *Servico) aoEstoqueOK(env evento.Envelope) error {
	var dados evento.DadosPedidoEstoqueOK
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	s.mudarStatus(dados.PedidoID, StatusEstoqueConfirmado, "itens reservados, seguindo para o pagamento")
	return nil
}

// aoEstoqueIndisponivel cancela o pedido e publica pedido.excluido.
func (s *Servico) aoEstoqueIndisponivel(ctx context.Context, env evento.Envelope) error {
	var dados evento.DadosEstoqueIndisponivel
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	s.mudarStatus(dados.PedidoID, StatusSemEstoque, dados.Motivo)
	return s.excluir(ctx, dados.PedidoID, "estoque indisponível: "+dados.Motivo)
}

// aoPagamentoPendente guarda a URL de checkout, que o frontend usa para abrir
// a página do provedor de pagamento.
func (s *Servico) aoPagamentoPendente(env evento.Envelope) error {
	var dados evento.DadosPagamentoPendente
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	detalhe := fmt.Sprintf("cobrança %s criada, aguardando o pagamento", dados.CobrancaID)
	pedido, err := s.registro.AguardarPagamento(dados.PedidoID, dados.URLCheckout, detalhe)
	s.notificar(dados.PedidoID, pedido, err)
	return nil
}

func (s *Servico) aoPagamentoAprovado(env evento.Envelope) error {
	var dados evento.DadosPagamentoAprovado
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	s.mudarStatus(dados.PedidoID, StatusPagamentoAprovado,
		fmt.Sprintf("transação %s, R$ %.2f", dados.TransacaoID, dados.Valor))
	return nil
}

// aoPagamentoRecusado cancela o pedido e publica pedido.excluido.
func (s *Servico) aoPagamentoRecusado(ctx context.Context, env evento.Envelope) error {
	var dados evento.DadosPagamentoRecusado
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	s.mudarStatus(dados.PedidoID, StatusPagamentoRecusado, dados.Motivo)
	return s.excluir(ctx, dados.PedidoID, "pagamento recusado: "+dados.Motivo)
}

func (s *Servico) aoPedidoEnviado(env evento.Envelope) error {
	var dados evento.DadosPedidoEnviado
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	s.mudarStatus(dados.PedidoID, StatusEnviado,
		fmt.Sprintf("nota %s, %s, rastreio %s", dados.NotaFiscal, dados.Transportadora, dados.CodigoRastreio))
	return nil
}

func (s *Servico) mudarStatus(pedidoID string, status Status, detalhe string) {
	pedido, err := s.registro.Atualizar(pedidoID, status, detalhe)
	s.notificar(pedidoID, pedido, err)
}

// notificar avisa o frontend da mudança. Pedido desconhecido ou já encerrado
// só gera log: o evento não é um erro, apenas não muda nada.
func (s *Servico) notificar(pedidoID string, pedido Pedido, err error) {
	switch {
	case errors.Is(err, ErrPedidoNaoEncontrado):
		log.Printf("evento para o pedido %s, que este Gateway não conhece", pedidoID)
	case errors.Is(err, ErrPedidoEncerrado):
		log.Printf("pedido %s já está %s: evento atrasado ignorado", pedidoID, pedido.Status)
	case err != nil:
		log.Printf("atualizando o pedido %s: %v", pedidoID, err)
	default:
		log.Printf("pedido %s → %s (%s)", pedidoID, pedido.Status, pedido.Detalhe)
		s.avisar(pedido)
	}
}

// excluir publica pedido.excluido, mesmo para pedido que este Gateway não
// conhece.
func (s *Servico) excluir(ctx context.Context, pedidoID, motivo string) error {
	return s.publicador.PublicarECommerce(ctx, evento.PedidoExcluido, evento.DadosPedidoExcluido{
		PedidoID: pedidoID,
		Motivo:   motivo,
	})
}

// Produtos lista os produtos com saldo, consultando o MS Estoque.
func (s *Servico) Produtos(ctx context.Context) ([]Produto, error) {
	produtos, err := s.estoque.Disponiveis(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEstoqueIndisponivel, err)
	}
	return produtos, nil
}

// PedidosDoCliente devolve os pedidos de um cliente.
func (s *Servico) PedidosDoCliente(clienteID string) []Pedido {
	return s.registro.DoCliente(clienteID)
}

// CriarPedido monta o pedido com nome e preço vindos do Estoque, registra e
// publica pedido.criado. A disponibilidade não é conferida aqui: quem decide
// é o Estoque, ao consumir o evento.
func (s *Servico) CriarPedido(ctx context.Context, clienteID, cliente string, solicitados []ItemSolicitado) (Pedido, error) {
	clienteID = strings.TrimSpace(clienteID)
	if clienteID == "" {
		return Pedido{}, fmt.Errorf("%w: cliente_id é obrigatório", ErrPedidoInvalido)
	}

	quantidades, ordem, err := somarItens(solicitados)
	if err != nil {
		return Pedido{}, err
	}

	itens := make([]evento.ItemPedido, 0, len(ordem))
	for _, produtoID := range ordem {
		produto, existe, err := s.estoque.Produto(ctx, produtoID)
		switch {
		case err != nil:
			return Pedido{}, fmt.Errorf("%w: %v", ErrEstoqueIndisponivel, err)
		case !existe:
			return Pedido{}, fmt.Errorf("%w: produto %s não existe", ErrPedidoInvalido, produtoID)
		}

		itens = append(itens, evento.ItemPedido{
			ProdutoID:  produto.ID,
			Nome:       produto.Nome,
			Quantidade: quantidades[produtoID],
			PrecoUnit:  produto.Preco,
		})
	}

	if strings.TrimSpace(cliente) == "" {
		cliente = "cliente"
	}

	pedido := s.registro.Criar(clienteID, cliente, itens)
	s.avisar(pedido)

	err = s.publicador.PublicarECommerce(ctx, evento.PedidoCriado, evento.DadosPedidoCriado{
		PedidoID: pedido.ID,
		Cliente:  pedido.Cliente,
		Itens:    pedido.Itens,
		Total:    pedido.Total,
	})
	if err != nil {
		s.mudarStatus(pedido.ID, StatusCancelado, "falha ao publicar no broker")
		return Pedido{}, fmt.Errorf("%w: %v", ErrBrokerIndisponivel, err)
	}

	log.Printf("pedido %s criado para %s: %d item(ns), total R$ %.2f", pedido.ID, pedido.Cliente, len(itens), pedido.Total)
	return pedido, nil
}

// somarItens junta as linhas do mesmo produto, preservando a ordem em que
// apareceram, e recusa lista vazia ou quantidade que não seja positiva.
func somarItens(solicitados []ItemSolicitado) (map[string]int, []string, error) {
	if len(solicitados) == 0 {
		return nil, nil, fmt.Errorf("%w: o pedido precisa de pelo menos um item", ErrPedidoInvalido)
	}

	quantidades := make(map[string]int, len(solicitados))
	var ordem []string
	for _, item := range solicitados {
		produtoID := strings.TrimSpace(item.ProdutoID)
		switch {
		case produtoID == "":
			return nil, nil, fmt.Errorf("%w: produto_id é obrigatório", ErrPedidoInvalido)
		case item.Quantidade <= 0:
			return nil, nil, fmt.Errorf("%w: a quantidade de %s deve ser positiva", ErrPedidoInvalido, produtoID)
		}

		if _, repetido := quantidades[produtoID]; !repetido {
			ordem = append(ordem, produtoID)
		}
		quantidades[produtoID] += item.Quantidade
	}
	return quantidades, ordem, nil
}

// CancelarPedido exclui, a pedido do cliente, um pedido que ainda não
// terminou: publica pedido.excluido para o Estoque devolver a reserva e o
// Pagamento cancelar a cobrança. Pedido de outro cliente é tratado como
// inexistente.
func (s *Servico) CancelarPedido(ctx context.Context, pedidoID, clienteID string) (Pedido, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pedido, existe := s.registro.Buscar(pedidoID)
	switch {
	case !existe, pedido.ClienteID != clienteID:
		return Pedido{}, ErrPedidoNaoEncontrado
	case pedido.Status.Encerrado():
		return pedido, ErrPedidoEncerrado
	}

	if err := s.excluir(ctx, pedidoID, "excluído pelo usuário"); err != nil {
		return Pedido{}, fmt.Errorf("%w: %v", ErrBrokerIndisponivel, err)
	}

	cancelado, err := s.registro.Atualizar(pedidoID, StatusCancelado, "excluído pelo usuário")
	if err != nil {
		return Pedido{}, err
	}
	log.Printf("pedido %s cancelado pelo cliente", pedidoID)
	s.avisar(cancelado)
	return cancelado, nil
}

// RegistrarInteresse publica interesse.promocao para o e-mail receber as
// promoções das categorias escolhidas. Quem guarda a inscrição é o MS
// Promoções.
func (s *Servico) RegistrarInteresse(ctx context.Context, email string, categorias []string) (evento.DadosInteressePromocao, error) {
	endereco, err := validarEmail(email)
	if err != nil {
		return evento.DadosInteressePromocao{}, err
	}
	escolhidas, err := validarCategorias(categorias)
	if err != nil {
		return evento.DadosInteressePromocao{}, err
	}

	dados := evento.DadosInteressePromocao{Email: endereco, Categorias: escolhidas}
	if err := s.publicador.PublicarECommerce(ctx, evento.InteressePromocao, dados); err != nil {
		return evento.DadosInteressePromocao{}, fmt.Errorf("%w: %v", ErrBrokerIndisponivel, err)
	}

	log.Printf("interesse de %s nas categorias %v publicado", endereco, escolhidas)
	return dados, nil
}

// CancelarInteresse publica interesse.cancelado para o e-mail parar de
// receber promoções.
func (s *Servico) CancelarInteresse(ctx context.Context, email string) (evento.DadosInteresseCancelado, error) {
	endereco, err := validarEmail(email)
	if err != nil {
		return evento.DadosInteresseCancelado{}, err
	}

	dados := evento.DadosInteresseCancelado{Email: endereco}
	if err := s.publicador.PublicarECommerce(ctx, evento.InteresseCancelado, dados); err != nil {
		return evento.DadosInteresseCancelado{}, fmt.Errorf("%w: %v", ErrBrokerIndisponivel, err)
	}

	log.Printf("cancelamento do interesse de %s publicado", endereco)
	return dados, nil
}

// validarEmail devolve o e-mail em minúsculas, sem espaços, e recusa o que
// não for um endereço simples (sem nome na frente).
func validarEmail(bruto string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(bruto))
	endereco, err := mail.ParseAddress(email)
	if err != nil || endereco.Address != email {
		return "", fmt.Errorf("%w: e-mail %q inválido", ErrInteresseInvalido, bruto)
	}
	return email, nil
}

// validarCategorias exige ao menos uma categoria existente e devolve as
// escolhidas sem repetição, na ordem do catálogo.
func validarCategorias(pedidas []string) ([]string, error) {
	if len(pedidas) == 0 {
		return nil, fmt.Errorf("%w: escolha ao menos uma categoria", ErrInteresseInvalido)
	}

	marcadas := make(map[string]bool, len(pedidas))
	for _, bruta := range pedidas {
		categoria := strings.ToUpper(strings.TrimSpace(bruta))
		if !slices.Contains(catalogo.Categorias, categoria) {
			return nil, fmt.Errorf("%w: categoria %q não existe", ErrInteresseInvalido, bruta)
		}
		marcadas[categoria] = true
	}

	escolhidas := make([]string, 0, len(marcadas))
	for _, categoria := range catalogo.Categorias {
		if marcadas[categoria] {
			escolhidas = append(escolhidas, categoria)
		}
	}
	return escolhidas, nil
}
