package entrega

import (
	"context"
	"ecommerce/internal/comum/evento"
	"log"
	"testing"
)

type publicadorMock struct {
	publicados []publicado
}

type publicado struct {
	chave string
	dados any
}

func (p *publicadorMock) PublicarECommerce(_ context.Context, chave string, dados any) error {
	p.publicados = append(p.publicados, publicado{chave: chave, dados: dados})
	return nil
}

func (p *publicadorMock) ultimo(t *testing.T) publicado {
	t.Helper()
	if len(p.publicados) == 0 {
		t.Fatal("o serviço não publicou nada")
	}
	return p.publicados[len(p.publicados)-1]
}

func TestGerarPedido(t *testing.T) {

	s := NovoServico(&publicadorMock{})

	items := []evento.ItemPedido{{
		ProdutoID:  "P001",
		Nome:       "Produto",
		Quantidade: 10,
		PrecoUnit:  10.0,
	}}

	payload := evento.DadosPagamentoAprovado{
		PedidoID:    "1234567",
		TransacaoID: "1234567",
		Valor:       100.00,
		Itens:       items,
	}

	dados := s.ProcessarEntrega(context.Background(), payload)

	log.Println(dados)
}
