package principal

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"ecommerce/internal/catalogo"
	"ecommerce/internal/evento"
)

const clienteTeste = "cliente-1"

type publicado struct {
	chave string
	dados any
}

type publicadorFalso struct {
	mu         sync.Mutex
	publicados []publicado
	falhas     int
}

func (p *publicadorFalso) PublicarECommerce(_ context.Context, chave string, dados any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.falhas > 0 {
		p.falhas--
		return errors.New("broker fora do ar")
	}
	p.publicados = append(p.publicados, publicado{chave: chave, dados: dados})
	return nil
}

func (p *publicadorFalso) chaves() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var lista []string
	for _, pub := range p.publicados {
		lista = append(lista, pub.chave)
	}
	return lista
}

type estoqueFalso struct {
	foraDoAr  bool
	promocoes map[string]float64
}

func (e *estoqueFalso) Disponiveis(context.Context) ([]Produto, error) {
	if e.foraDoAr {
		return nil, errors.New("conexão recusada")
	}
	produtos := []Produto{}
	for _, p := range catalogo.Produtos {
		produtos = append(produtos, e.produtoDoCatalogo(p))
	}
	return produtos, nil
}

func (e *estoqueFalso) Produto(_ context.Context, id string) (Produto, bool, error) {
	if e.foraDoAr {
		return Produto{}, false, errors.New("conexão recusada")
	}
	p, existe := catalogo.Buscar(id)
	if !existe {
		return Produto{}, false, nil
	}
	return e.produtoDoCatalogo(p), true, nil
}

func (e *estoqueFalso) produtoDoCatalogo(p catalogo.Produto) Produto {
	produto := Produto{ID: p.ID, Nome: p.Nome, Categoria: p.Categoria, Preco: p.Preco, Quantidade: p.QuantidadeInicial}
	if promocional, existe := e.promocoes[p.ID]; existe {
		produto.PrecoOriginal = produto.Preco
		produto.Preco = promocional
	}
	return produto
}

func montarServico(t *testing.T) (*Servico, *Registro, *publicadorFalso, *estoqueFalso) {
	t.Helper()
	registro := NovoRegistro()
	pub := &publicadorFalso{}
	estoque := &estoqueFalso{}
	return NovoServico(registro, estoque, pub, nil), registro, pub, estoque
}

// pedidoNoRegistro cria um pedido e devolve o ID dele.
func pedidoNoRegistro(t *testing.T, registro *Registro) string {
	t.Helper()
	pedido := registro.Criar(clienteTeste, "Daniel", []evento.ItemPedido{
		{ProdutoID: "P001", Nome: "Notebook Gamer 16GB", Quantidade: 1, PrecoUnit: 4500},
	})
	return pedido.ID
}

func entregar(t *testing.T, s *Servico, tipo, produtor string, dados any) {
	t.Helper()
	env, err := evento.NovoEnvelope(tipo, produtor, dados)
	if err != nil {
		t.Fatalf("montando %s: %v", tipo, err)
	}
	if err := s.Tratar(context.Background(), env); err != nil {
		t.Fatalf("tratando %s: %v", tipo, err)
	}
}

func exigirStatus(t *testing.T, registro *Registro, pedidoID string, esperado Status) {
	t.Helper()
	pedido, existe := registro.Buscar(pedidoID)
	if !existe {
		t.Fatalf("pedido %s sumiu do registro", pedidoID)
	}
	if pedido.Status != esperado {
		t.Fatalf("status de %s: esperava %q, veio %q", pedidoID, esperado, pedido.Status)
	}
}

func TestEstoqueOKMarcaReservadoENaoCancela(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PedidoEstoqueOK, evento.ServicoEstoque, evento.DadosPedidoEstoqueOK{
		PedidoID: id,
		Total:    4500,
	})

	exigirStatus(t, registro, id, StatusEstoqueConfirmado)
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestEstoqueIndisponivelCancelaEPublicaPedidoExcluido(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.EstoqueIndisponivel, evento.ServicoEstoque, evento.DadosEstoqueIndisponivel{
		PedidoID: id,
		Motivo:   "estoque insuficiente de Air Fryer 5L",
	})

	exigirStatus(t, registro, id, StatusSemEstoque)

	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.PedidoExcluido {
		t.Fatalf("esperava publicar %s, publicou %v", evento.PedidoExcluido, pub.chaves())
	}
	dados, ok := pub.publicados[0].dados.(evento.DadosPedidoExcluido)
	if !ok {
		t.Fatalf("payload inesperado: %T", pub.publicados[0].dados)
	}
	if dados.PedidoID != id {
		t.Fatalf("excluiu o pedido errado: %q", dados.PedidoID)
	}
}

func TestPagamentoPendenteGuardaURLDeCheckout(t *testing.T) {
	s, registro, _, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PagamentoPendente, evento.ServicoPagamento, evento.DadosPagamentoPendente{
		PedidoID:    id,
		CobrancaID:  "COB-1",
		URLCheckout: "http://mock.teste/checkout/COB-1",
		Valor:       4500,
	})

	exigirStatus(t, registro, id, StatusAguardandoPagamento)
	pedido, _ := registro.Buscar(id)
	if pedido.URLCheckout != "http://mock.teste/checkout/COB-1" {
		t.Fatalf("URL de checkout não foi guardada: %q", pedido.URLCheckout)
	}
}

func TestPagamentoAprovadoMarcaAprovadoENaoCancela(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PagamentoAprovado, evento.ServicoPagamento, evento.DadosPagamentoAprovado{
		PedidoID:    id,
		TransacaoID: "TX-abc123",
		Valor:       4500,
	})

	exigirStatus(t, registro, id, StatusPagamentoAprovado)
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestPagamentoRecusadoCancelaEPublicaPedidoExcluido(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PagamentoRecusado, evento.ServicoPagamento, evento.DadosPagamentoRecusado{
		PedidoID: id,
		Valor:    4500,
		Motivo:   "saldo insuficiente",
	})

	exigirStatus(t, registro, id, StatusPagamentoRecusado)
	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.PedidoExcluido {
		t.Fatalf("esperava publicar %s, publicou %v", evento.PedidoExcluido, pub.chaves())
	}
}

func TestPedidoEnviadoMarcaEnviado(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PedidoEnviado, evento.ServicoEntrega, evento.DadosPedidoEnviado{
		PedidoID:       id,
		NotaFiscal:     "NF-0001",
		Transportadora: "Correios",
		CodigoRastreio: "BR123456789",
	})

	exigirStatus(t, registro, id, StatusEnviado)
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestPedidoDesconhecidoAindaAssimPublicaExclusao(t *testing.T) {
	s, _, pub, _ := montarServico(t)

	entregar(t, s, evento.PagamentoRecusado, evento.ServicoPagamento, evento.DadosPagamentoRecusado{
		PedidoID: "PED-de-outra-sessao",
		Motivo:   "cartão expirado",
	})

	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.PedidoExcluido {
		t.Fatalf("esperava publicar %s, publicou %v", evento.PedidoExcluido, pub.chaves())
	}
}

func TestPedidoEncerradoIgnoraEventoAtrasado(t *testing.T) {
	s, registro, _, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	if _, err := s.CancelarPedido(context.Background(), id, clienteTeste); err != nil {
		t.Fatalf("cancelando: %v", err)
	}
	entregar(t, s, evento.PagamentoPendente, evento.ServicoPagamento, evento.DadosPagamentoPendente{
		PedidoID:    id,
		URLCheckout: "http://mock.teste/checkout/COB-1",
	})

	exigirStatus(t, registro, id, StatusCancelado)
	if pedido, _ := registro.Buscar(id); pedido.URLCheckout != "" {
		t.Fatalf("um pedido cancelado não deveria ganhar URL de pagamento: %q", pedido.URLCheckout)
	}
}

func TestEventoDeOutroAssuntoEIgnorado(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PedidoCriado, evento.ServicoPrincipal, evento.DadosPedidoCriado{PedidoID: id})

	exigirStatus(t, registro, id, StatusAguardandoEstoque)
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestHistoricoGuardaACadeiaDeStatus(t *testing.T) {
	s, registro, _, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	entregar(t, s, evento.PedidoEstoqueOK, evento.ServicoEstoque, evento.DadosPedidoEstoqueOK{PedidoID: id})
	entregar(t, s, evento.PagamentoPendente, evento.ServicoPagamento, evento.DadosPagamentoPendente{PedidoID: id})
	entregar(t, s, evento.PagamentoAprovado, evento.ServicoPagamento, evento.DadosPagamentoAprovado{PedidoID: id})
	entregar(t, s, evento.PedidoEnviado, evento.ServicoEntrega, evento.DadosPedidoEnviado{PedidoID: id})

	pedido, _ := registro.Buscar(id)
	esperado := []Status{
		StatusAguardandoEstoque,
		StatusEstoqueConfirmado,
		StatusAguardandoPagamento,
		StatusPagamentoAprovado,
		StatusEnviado,
	}

	if len(pedido.Historico) != len(esperado) {
		t.Fatalf("esperava %d marcas no histórico, vieram %d", len(esperado), len(pedido.Historico))
	}
	for i, status := range esperado {
		if pedido.Historico[i].Status != status {
			t.Fatalf("marca %d: esperava %q, veio %q", i, status, pedido.Historico[i].Status)
		}
	}
}

func TestListarDevolveCopias(t *testing.T) {
	registro := NovoRegistro()
	id := pedidoNoRegistro(t, registro)

	lista := registro.Listar()
	lista[0].Status = StatusEnviado

	exigirStatus(t, registro, id, StatusAguardandoEstoque)
}

func TestIdsDePedidoNaoSeRepetem(t *testing.T) {
	registro := NovoRegistro()
	vistos := make(map[string]bool)

	for i := 0; i < 500; i++ {
		pedido := registro.Criar(clienteTeste, "Daniel", nil)
		if vistos[pedido.ID] {
			t.Fatalf("identificador repetido: %s", pedido.ID)
		}
		vistos[pedido.ID] = true
	}
}

func TestCriarPedidoUsaNomeEPrecoDoEstoque(t *testing.T) {
	s, _, pub, _ := montarServico(t)

	pedido, err := s.CriarPedido(context.Background(), clienteTeste, "Daniel", []ItemSolicitado{
		{ProdutoID: "P001", Quantidade: 1},
		{ProdutoID: "P004", Quantidade: 2},
		{ProdutoID: "P001", Quantidade: 1},
	})
	if err != nil {
		t.Fatalf("criando pedido: %v", err)
	}

	if len(pedido.Itens) != 2 || pedido.Itens[0].Quantidade != 2 || pedido.Itens[0].PrecoUnit != 4500 ||
		pedido.Itens[1].Nome != "Cafeteira expresso" {
		t.Fatalf("itens montados errado: %+v", pedido.Itens)
	}
	if pedido.Total != 9560 || pedido.Status != StatusAguardandoEstoque {
		t.Fatalf("pedido inesperado: total %.2f, status %s", pedido.Total, pedido.Status)
	}

	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.PedidoCriado {
		t.Fatalf("esperava publicar %s, publicou %v", evento.PedidoCriado, pub.chaves())
	}
	if dados := pub.publicados[0].dados.(evento.DadosPedidoCriado); dados.PedidoID != pedido.ID || dados.Total != 9560 {
		t.Fatalf("pedido.criado não bate com o pedido: %+v", dados)
	}
}

func TestCriarPedidoUsaOPrecoPromocionalDoEstoque(t *testing.T) {
	s, _, pub, estoque := montarServico(t)
	estoque.promocoes = map[string]float64{"P005": 167.2}

	pedido, err := s.CriarPedido(context.Background(), clienteTeste, "Daniel",
		[]ItemSolicitado{{ProdutoID: "P005", Quantidade: 2}})
	if err != nil {
		t.Fatalf("criando pedido: %v", err)
	}

	if pedido.Itens[0].PrecoUnit != 167.2 || pedido.Total != 334.4 {
		t.Fatalf("o pedido deveria usar o preço promocional: unitário %.2f, total %.2f",
			pedido.Itens[0].PrecoUnit, pedido.Total)
	}
	if dados := pub.publicados[0].dados.(evento.DadosPedidoCriado); dados.Total != pedido.Total {
		t.Fatalf("pedido.criado com total diferente do pedido: %.2f", dados.Total)
	}
}

func TestCriarPedidoInvalidoNaoPublica(t *testing.T) {
	casos := []struct {
		nome      string
		clienteID string
		itens     []ItemSolicitado
	}{
		{"sem cliente_id", "", []ItemSolicitado{{ProdutoID: "P001", Quantidade: 1}}},
		{"sem itens", clienteTeste, nil},
		{"quantidade zero", clienteTeste, []ItemSolicitado{{ProdutoID: "P001", Quantidade: 0}}},
		{"produto inexistente", clienteTeste, []ItemSolicitado{{ProdutoID: "P999", Quantidade: 1}}},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, registro, pub, _ := montarServico(t)

			_, err := s.CriarPedido(context.Background(), c.clienteID, "Daniel", c.itens)

			if !errors.Is(err, ErrPedidoInvalido) {
				t.Fatalf("esperava ErrPedidoInvalido, veio %v", err)
			}
			if registro.Quantidade() != 0 || len(pub.publicados) != 0 {
				t.Fatal("um pedido inválido não deveria ser registrado nem publicado")
			}
		})
	}
}

func TestCriarPedidoComEstoqueForaDoAr(t *testing.T) {
	s, registro, _, estoque := montarServico(t)
	estoque.foraDoAr = true

	_, err := s.CriarPedido(context.Background(), clienteTeste, "Daniel", []ItemSolicitado{{ProdutoID: "P001", Quantidade: 1}})

	if !errors.Is(err, ErrEstoqueIndisponivel) {
		t.Fatalf("esperava ErrEstoqueIndisponivel, veio %v", err)
	}
	if registro.Quantidade() != 0 {
		t.Fatal("sem o Estoque, nenhum pedido deveria ser registrado")
	}
}

func TestCriarPedidoComBrokerForaDoArCancelaOPedido(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	pub.falhas = 1

	_, err := s.CriarPedido(context.Background(), clienteTeste, "Daniel", []ItemSolicitado{{ProdutoID: "P001", Quantidade: 1}})

	if !errors.Is(err, ErrBrokerIndisponivel) {
		t.Fatalf("esperava ErrBrokerIndisponivel, veio %v", err)
	}
	lista := registro.Listar()
	if len(lista) != 1 || lista[0].Status != StatusCancelado {
		t.Fatalf("o pedido que não chegou ao broker deveria ficar cancelado: %+v", lista)
	}
}

func TestCancelarPedidoPublicaExclusaoEMarcaCancelado(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	pedido, err := s.CancelarPedido(context.Background(), id, clienteTeste)

	if err != nil {
		t.Fatalf("cancelando: %v", err)
	}
	if pedido.Status != StatusCancelado {
		t.Fatalf("esperava %s, veio %s", StatusCancelado, pedido.Status)
	}
	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.PedidoExcluido {
		t.Fatalf("esperava publicar %s, publicou %v", evento.PedidoExcluido, pub.chaves())
	}
}

func TestCancelarPedidoDeOutroClienteOuInexistente(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)

	for _, c := range []struct{ pedidoID, clienteID string }{
		{id, "cliente-2"},
		{"PED-nada", clienteTeste},
	} {
		if _, err := s.CancelarPedido(context.Background(), c.pedidoID, c.clienteID); !errors.Is(err, ErrPedidoNaoEncontrado) {
			t.Fatalf("cancelar %s como %s: esperava ErrPedidoNaoEncontrado, veio %v", c.pedidoID, c.clienteID, err)
		}
	}

	exigirStatus(t, registro, id, StatusAguardandoEstoque)
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestCancelarPedidoEncerradoDaConflito(t *testing.T) {
	s, registro, pub, _ := montarServico(t)
	id := pedidoNoRegistro(t, registro)
	entregar(t, s, evento.PedidoEnviado, evento.ServicoEntrega, evento.DadosPedidoEnviado{PedidoID: id})

	_, err := s.CancelarPedido(context.Background(), id, clienteTeste)

	if !errors.Is(err, ErrPedidoEncerrado) {
		t.Fatalf("esperava ErrPedidoEncerrado, veio %v", err)
	}
	if len(pub.publicados) != 0 {
		t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
	}
}

func TestRegistrarInteresseNormalizaEPublica(t *testing.T) {
	s, _, pub, _ := montarServico(t)

	dados, err := s.RegistrarInteresse(context.Background(), "  Fulano@Exemplo.com ", []string{"c", "A", "a"})

	if err != nil {
		t.Fatalf("registrando interesse: %v", err)
	}
	if dados.Email != "fulano@exemplo.com" || !slices.Equal(dados.Categorias, []string{"A", "C"}) {
		t.Fatalf("interesse não normalizado: %+v", dados)
	}
	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.InteressePromocao {
		t.Fatalf("esperava publicar %s, publicou %v", evento.InteressePromocao, pub.chaves())
	}
}

func TestInteresseInvalidoNaoPublica(t *testing.T) {
	casos := []struct {
		nome       string
		email      string
		categorias []string
	}{
		{"e-mail sem arroba", "fulano", []string{"A"}},
		{"e-mail com nome na frente", "Fulano <fulano@exemplo.com>", []string{"A"}},
		{"sem categorias", "fulano@exemplo.com", nil},
		{"categoria inexistente", "fulano@exemplo.com", []string{"Z"}},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, _, pub, _ := montarServico(t)

			_, err := s.RegistrarInteresse(context.Background(), c.email, c.categorias)

			if !errors.Is(err, ErrInteresseInvalido) {
				t.Fatalf("esperava ErrInteresseInvalido, veio %v", err)
			}
			if len(pub.publicados) != 0 {
				t.Fatalf("não deveria publicar nada, publicou %v", pub.chaves())
			}
		})
	}
}

func TestCancelarInteressePublicaCancelamento(t *testing.T) {
	s, _, pub, _ := montarServico(t)

	dados, err := s.CancelarInteresse(context.Background(), "Fulano@Exemplo.com")

	if err != nil {
		t.Fatalf("cancelando interesse: %v", err)
	}
	if dados.Email != "fulano@exemplo.com" {
		t.Fatalf("e-mail não normalizado: %q", dados.Email)
	}
	if len(pub.publicados) != 1 || pub.publicados[0].chave != evento.InteresseCancelado {
		t.Fatalf("esperava publicar %s, publicou %v", evento.InteresseCancelado, pub.chaves())
	}

	if _, err := s.CancelarInteresse(context.Background(), "fulano"); !errors.Is(err, ErrInteresseInvalido) {
		t.Fatalf("e-mail inválido: esperava ErrInteresseInvalido, veio %v", err)
	}
}
