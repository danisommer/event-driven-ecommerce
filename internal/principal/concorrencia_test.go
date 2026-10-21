package principal

import (
	"context"
	"sync"
	"testing"

	"ecommerce/internal/evento"
)

// Rodar com o detector de corrida:
//
//	go test -race ./internal/principal/
func TestRegistroAguentaHTTPEConsumidorAoMesmoTempo(t *testing.T) {
	registro := NovoRegistro()
	servico := NovoServico(registro, &estoqueFalso{}, &publicadorFalso{}, nil)

	const pedidosPorGoroutine = 50

	ids := make(chan string, pedidosPorGoroutine*2)

	var grupo sync.WaitGroup

	for i := 0; i < 2; i++ {
		grupo.Add(1)
		go func() {
			defer grupo.Done()
			for j := 0; j < pedidosPorGoroutine; j++ {
				pedido := registro.Criar(clienteTeste, "Daniel", []evento.ItemPedido{
					{ProdutoID: "P001", Nome: "Notebook", Quantidade: 1, PrecoUnit: 4500},
				})
				ids <- pedido.ID
			}
		}()
	}

	go func() {
		grupo.Wait()
		close(ids)
	}()

	var consumo sync.WaitGroup
	consumo.Add(3)

	go func() {
		defer consumo.Done()
		for id := range ids {
			env, err := evento.NovoEnvelope(evento.PedidoEstoqueOK, evento.ServicoEstoque,
				evento.DadosPedidoEstoqueOK{PedidoID: id})
			if err != nil {
				t.Errorf("montando envelope: %v", err)
				return
			}
			if err := servico.Tratar(context.Background(), env); err != nil {
				t.Errorf("tratando evento: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 2; i++ {
		go func() {
			defer consumo.Done()
			for j := 0; j < 200; j++ {
				for _, pedido := range registro.DoCliente(clienteTeste) {
					_ = pedido.Status
				}
			}
		}()
	}

	consumo.Wait()

	if registro.Quantidade() != pedidosPorGoroutine*2 {
		t.Fatalf("esperava %d pedidos, vieram %d", pedidosPorGoroutine*2, registro.Quantidade())
	}
}
