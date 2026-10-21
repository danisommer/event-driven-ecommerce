package pagamento

import (
	"context"
	"errors"
	"testing"

	"ecommerce/internal/evento"
)

type publicado struct {
	chave string
	dados any
}

type publicadorFalso struct {
	publicados []publicado
	falhas     int
}

func (p *publicadorFalso) PublicarECommerce(_ context.Context, chave string, dados any) error {
	if p.falhas > 0 {
		p.falhas--
		return errors.New("broker fora do ar")
	}
	p.publicados = append(p.publicados, publicado{chave: chave, dados: dados})
	return nil
}

func (p *publicadorFalso) ultimo(t *testing.T) publicado {
	t.Helper()
	if len(p.publicados) == 0 {
		t.Fatal("o serviço não publicou nada")
	}
	return p.publicados[len(p.publicados)-1]
}

type provedorFalso struct {
	pedidos []string
	falhar  bool
}

func (p *provedorFalso) CriarCobranca(_ context.Context, pedidoID string, _ float64) (CobrancaCriada, error) {
	p.pedidos = append(p.pedidos, pedidoID)
	if p.falhar {
		return CobrancaCriada{}, errors.New("conexão recusada")
	}
	return CobrancaCriada{ID: "COB-" + pedidoID, URLCheckout: "http://mock.teste/checkout/COB-" + pedidoID}, nil
}

func montar(t *testing.T) (*Servico, *provedorFalso, *publicadorFalso) {
	t.Helper()
	provedor := &provedorFalso{}
	pub := &publicadorFalso{}
	return NovoServico(provedor, pub), provedor, pub
}

func estoqueOK(t *testing.T, pedidoID string, total float64) evento.Envelope {
	t.Helper()
	env, err := evento.NovoEnvelope(evento.PedidoEstoqueOK, evento.ServicoEstoque, evento.DadosPedidoEstoqueOK{
		PedidoID: pedidoID,
		Itens:    []evento.ItemPedido{{ProdutoID: "P001", Nome: "Notebook", Quantidade: 1, PrecoUnit: total}},
		Total:    total,
	})
	if err != nil {
		t.Fatalf("montando pedido.estoque_ok: %v", err)
	}
	return env
}

func pedidoExcluido(t *testing.T, pedidoID string) evento.Envelope {
	t.Helper()
	env, err := evento.NovoEnvelope(evento.PedidoExcluido, evento.ServicoPrincipal, evento.DadosPedidoExcluido{
		PedidoID: pedidoID,
		Motivo:   "excluído pelo usuário",
	})
	if err != nil {
		t.Fatalf("montando pedido.excluido: %v", err)
	}
	return env
}

func tratar(t *testing.T, s *Servico, env evento.Envelope) {
	t.Helper()
	if err := s.Tratar(context.Background(), env); err != nil {
		t.Fatalf("tratando %s: %v", env.Tipo, err)
	}
}

func decidir(t *testing.T, s *Servico, pedidoID, decisao string) {
	t.Helper()
	if err := s.Decidir(context.Background(), "COB-"+pedidoID, pedidoID, decisao); err != nil {
		t.Fatalf("decidindo %s para %s: %v", decisao, pedidoID, err)
	}
}

func TestEstoqueOKCriaCobrancaEPublicaPendente(t *testing.T) {
	s, provedor, pub := montar(t)

	tratar(t, s, estoqueOK(t, "PED-1", 4500))

	if len(provedor.pedidos) != 1 || provedor.pedidos[0] != "PED-1" {
		t.Fatalf("esperava uma cobrança para PED-1, o provedor recebeu %v", provedor.pedidos)
	}

	ultimo := pub.ultimo(t)
	if ultimo.chave != evento.PagamentoPendente {
		t.Fatalf("esperava %s, veio %s", evento.PagamentoPendente, ultimo.chave)
	}
	dados := ultimo.dados.(evento.DadosPagamentoPendente)
	if dados.CobrancaID != "COB-PED-1" || dados.URLCheckout != "http://mock.teste/checkout/COB-PED-1" || dados.Valor != 4500 {
		t.Fatalf("pagamento.pendente incompleto: %+v", dados)
	}
}

func TestReentregaDoEstoqueOKNaoCriaOutraCobranca(t *testing.T) {
	s, provedor, pub := montar(t)

	env := estoqueOK(t, "PED-2", 150)
	tratar(t, s, env)
	tratar(t, s, env)

	if len(provedor.pedidos) != 1 || len(pub.publicados) != 1 {
		t.Fatalf("reentrega gerou %d cobrança(s) e %d evento(s)", len(provedor.pedidos), len(pub.publicados))
	}
}

func TestProvedorForaDoArPublicaRecusado(t *testing.T) {
	s, provedor, pub := montar(t)
	provedor.falhar = true

	tratar(t, s, estoqueOK(t, "PED-3", 150))

	ultimo := pub.ultimo(t)
	if ultimo.chave != evento.PagamentoRecusado {
		t.Fatalf("esperava %s, veio %s", evento.PagamentoRecusado, ultimo.chave)
	}
	if motivo := ultimo.dados.(evento.DadosPagamentoRecusado).Motivo; motivo != motivoSemCobranca {
		t.Fatalf("motivo inesperado: %q", motivo)
	}
}

func TestWebhookAprovadoPublicaAprovadoComItens(t *testing.T) {
	s, _, pub := montar(t)
	tratar(t, s, estoqueOK(t, "PED-4", 4500))

	decidir(t, s, "PED-4", DecisaoAprovado)

	ultimo := pub.ultimo(t)
	if ultimo.chave != evento.PagamentoAprovado {
		t.Fatalf("esperava %s, veio %s", evento.PagamentoAprovado, ultimo.chave)
	}
	dados := ultimo.dados.(evento.DadosPagamentoAprovado)
	if dados.TransacaoID != "COB-PED-4" || dados.Valor != 4500 || len(dados.Itens) != 1 {
		t.Fatalf("pagamento.aprovado incompleto (a Entrega precisa dos itens): %+v", dados)
	}
}

func TestWebhookRecusadoPublicaRecusado(t *testing.T) {
	s, _, pub := montar(t)
	tratar(t, s, estoqueOK(t, "PED-5", 4500))

	decidir(t, s, "PED-5", DecisaoRecusado)

	ultimo := pub.ultimo(t)
	if ultimo.chave != evento.PagamentoRecusado {
		t.Fatalf("esperava %s, veio %s", evento.PagamentoRecusado, ultimo.chave)
	}
	if motivo := ultimo.dados.(evento.DadosPagamentoRecusado).Motivo; motivo != motivoRecusado {
		t.Fatalf("motivo inesperado: %q", motivo)
	}
}

func TestWebhookRepetidoNaoPublicaDeNovo(t *testing.T) {
	s, _, pub := montar(t)
	tratar(t, s, estoqueOK(t, "PED-6", 150))

	decidir(t, s, "PED-6", DecisaoAprovado)
	decidir(t, s, "PED-6", DecisaoRecusado)

	if len(pub.publicados) != 2 {
		t.Fatalf("esperava pendente + aprovado, vieram %d eventos", len(pub.publicados))
	}
}

func TestWebhookInvalidoDevolveErroSemPublicar(t *testing.T) {
	casos := []struct {
		nome       string
		cobrancaID string
		pedidoID   string
		decisao    string
		esperado   error
	}{
		{"status desconhecido", "COB-PED-7", "PED-7", "TALVEZ", ErrDecisaoInvalida},
		{"cobrança desconhecida", "COB-NADA", "PED-7", DecisaoAprovado, ErrCobrancaDesconhecida},
		{"pedido de outra cobrança", "COB-PED-7", "PED-OUTRO", DecisaoAprovado, ErrPedidoDivergente},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s, _, pub := montar(t)
			tratar(t, s, estoqueOK(t, "PED-7", 150))

			err := s.Decidir(context.Background(), c.cobrancaID, c.pedidoID, c.decisao)

			if !errors.Is(err, c.esperado) {
				t.Fatalf("esperava %v, veio %v", c.esperado, err)
			}
			if len(pub.publicados) != 1 {
				t.Fatalf("um aviso inválido não deveria publicar nada, vieram %d eventos", len(pub.publicados))
			}
		})
	}
}

func TestPedidoExcluidoCancelaCobrancaPendente(t *testing.T) {
	s, _, pub := montar(t)
	tratar(t, s, estoqueOK(t, "PED-8", 4500))

	tratar(t, s, pedidoExcluido(t, "PED-8"))
	decidir(t, s, "PED-8", DecisaoAprovado)

	if len(pub.publicados) != 1 {
		t.Fatalf("aprovação de pedido cancelado não deveria publicar, vieram %d eventos", len(pub.publicados))
	}
}

func TestPedidoExcluidoAntesDoEstoqueOKImpedeACobranca(t *testing.T) {
	s, provedor, pub := montar(t)

	tratar(t, s, pedidoExcluido(t, "PED-9"))
	tratar(t, s, estoqueOK(t, "PED-9", 4500))

	if len(provedor.pedidos) != 0 || len(pub.publicados) != 0 {
		t.Fatalf("pedido já excluído gerou %d cobrança(s) e %d evento(s)", len(provedor.pedidos), len(pub.publicados))
	}
}

func TestFalhaAoPublicarDecisaoPermiteNovaTentativa(t *testing.T) {
	s, _, pub := montar(t)
	tratar(t, s, estoqueOK(t, "PED-10", 150))
	pub.falhas = 1

	if err := s.Decidir(context.Background(), "COB-PED-10", "PED-10", DecisaoAprovado); err == nil {
		t.Fatal("com o broker fora do ar, Decidir deveria devolver erro")
	}

	decidir(t, s, "PED-10", DecisaoAprovado)
	if chave := pub.ultimo(t).chave; chave != evento.PagamentoAprovado {
		t.Fatalf("a nova tentativa deveria publicar %s, veio %s", evento.PagamentoAprovado, chave)
	}
}

func TestEventoDeOutroAssuntoEIgnorado(t *testing.T) {
	s, provedor, pub := montar(t)

	env, err := evento.NovoEnvelope(evento.PagamentoAprovado, evento.ServicoPagamento,
		evento.DadosPagamentoAprovado{PedidoID: "PED-11"})
	if err != nil {
		t.Fatalf("montando envelope: %v", err)
	}
	tratar(t, s, env)

	if len(provedor.pedidos) != 0 || len(pub.publicados) != 0 {
		t.Fatal("o Pagamento reagiu a um evento que não é assunto dele")
	}
}
