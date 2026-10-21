package principal

import "testing"

func pedidoDo(clienteID string) Pedido {
	return Pedido{ID: "PED-1", ClienteID: clienteID, Status: StatusAguardandoEstoque}
}

func TestHubEntregaSoParaAsConexoesDoDono(t *testing.T) {
	hub := NovoHub()
	aba1, sair1 := hub.Assinar("cliente-1")
	defer sair1()
	aba2, sair2 := hub.Assinar("cliente-1")
	defer sair2()
	outro, sair3 := hub.Assinar("cliente-2")
	defer sair3()

	hub.Publicar(pedidoDo("cliente-1"))

	for nome, canal := range map[string]<-chan Pedido{"aba 1": aba1, "aba 2": aba2} {
		select {
		case pedido := <-canal:
			if pedido.ID != "PED-1" {
				t.Fatalf("a %s recebeu o pedido errado: %s", nome, pedido.ID)
			}
		default:
			t.Fatalf("a %s do dono não recebeu o pedido", nome)
		}
	}

	select {
	case pedido := <-outro:
		t.Fatalf("outro cliente recebeu o pedido %s", pedido.ID)
	default:
	}
}

func TestHubDerrubaConexaoQueNaoAcompanha(t *testing.T) {
	hub := NovoHub()
	canal, sair := hub.Assinar("cliente-1")
	defer sair()

	for i := 0; i <= tamanhoFilaSSE; i++ {
		hub.Publicar(pedidoDo("cliente-1"))
	}

	recebidos := 0
	for range canal {
		recebidos++
	}

	if recebidos != tamanhoFilaSSE {
		t.Fatalf("esperava %d mudanças antes da queda, vieram %d", tamanhoFilaSSE, recebidos)
	}
	if n := hub.Conexoes("cliente-1"); n != 0 {
		t.Fatalf("a conexão lenta deveria ter saído do hub, restaram %d", n)
	}
}

func TestEncerrarAssinaturaFechaOCanalUmaVezSo(t *testing.T) {
	hub := NovoHub()
	canal, sair := hub.Assinar("cliente-1")

	sair()
	sair()

	if _, aberto := <-canal; aberto {
		t.Fatal("o canal deveria estar fechado")
	}
	hub.Publicar(pedidoDo("cliente-1"))
}
