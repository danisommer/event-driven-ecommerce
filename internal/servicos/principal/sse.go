package principal

import (
	"log"
	"sync"
)

// tamanhoFilaSSE é quantas mudanças uma conexão pode ter pendentes antes de
// ser considerada lenta demais.
const tamanhoFilaSSE = 16

// Hub entrega cada mudança de pedido às conexões SSE abertas pelo dono do
// pedido. Um cliente pode ter várias conexões, uma por aba.
type Hub struct {
	mu       sync.Mutex
	conexoes map[string]map[chan Pedido]struct{}
}

// NovoHub cria um hub sem conexões.
func NovoHub() *Hub {
	return &Hub{conexoes: make(map[string]map[chan Pedido]struct{})}
}

// Assinar abre uma conexão para o cliente. Devolve o canal das mudanças e a
// função que encerra a assinatura. O canal é fechado quando a assinatura
// acaba ou quando a conexão fica para trás.
func (h *Hub) Assinar(clienteID string) (<-chan Pedido, func()) {
	canal := make(chan Pedido, tamanhoFilaSSE)

	h.mu.Lock()
	if h.conexoes[clienteID] == nil {
		h.conexoes[clienteID] = make(map[chan Pedido]struct{})
	}
	h.conexoes[clienteID][canal] = struct{}{}
	h.mu.Unlock()

	return canal, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.remover(clienteID, canal)
	}
}

// Publicar entrega o pedido a todas as conexões do dono dele, sem esperar.
// Uma conexão com a fila cheia é derrubada: o navegador reconecta e busca o
// estado atual, em vez de o consumidor do RabbitMQ ficar travado esperando.
func (h *Hub) Publicar(pedido Pedido) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for canal := range h.conexoes[pedido.ClienteID] {
		select {
		case canal <- pedido:
		default:
			log.Printf("SSE: uma conexão do cliente %s não acompanhou as mudanças e foi derrubada", pedido.ClienteID)
			h.remover(pedido.ClienteID, canal)
		}
	}
}

// Conexoes diz quantas conexões o cliente tem abertas.
func (h *Hub) Conexoes(clienteID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conexoes[clienteID])
}

// remover tira a conexão do hub e fecha o canal, uma vez só. Quem chama
// segura h.mu.
func (h *Hub) remover(clienteID string, canal chan Pedido) {
	conexoes := h.conexoes[clienteID]
	if _, aberta := conexoes[canal]; !aberta {
		return
	}

	delete(conexoes, canal)
	close(canal)
	if len(conexoes) == 0 {
		delete(h.conexoes, clienteID)
	}
}
