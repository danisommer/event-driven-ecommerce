// Package principal implementa o microsserviço Principal, o API Gateway da
// loja: expõe a API REST consumida pelo frontend, transforma as ações do
// usuário em eventos no RabbitMQ e avisa o frontend, por SSE, de cada mudança
// de status dos pedidos.
//
// Publica pedido.criado, pedido.excluido, interesse.promocao e
// interesse.cancelado. Consome pedido.estoque_ok, estoque.indisponivel,
// pagamento.pendente, pagamento.aprovado, pagamento.recusado e
// pedido.enviado.
package principal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"ecommerce/internal/evento"
)

// Publicador é o pedaço da mensageria que o Principal usa.
type Publicador interface {
	PublicarECommerce(ctx context.Context, routingKey string, dados any) error
}

// Status é o estágio em que um pedido está. O valor é um código estável, lido
// pelo frontend; o texto exibido ao usuário fica a cargo de quem mostra.
type Status string

const (
	StatusAguardandoEstoque   Status = "aguardando_estoque"
	StatusEstoqueConfirmado   Status = "estoque_confirmado"
	StatusAguardandoPagamento Status = "aguardando_pagamento"
	StatusPagamentoAprovado   Status = "pagamento_aprovado"
	StatusEnviado             Status = "enviado"
	StatusSemEstoque          Status = "sem_estoque"
	StatusPagamentoRecusado   Status = "pagamento_recusado"
	StatusCancelado           Status = "cancelado"
)

// Encerrado diz se o pedido chegou ao fim da linha, para bem ou para mal.
// Um pedido encerrado não aceita mais exclusão.
func (s Status) Encerrado() bool {
	switch s {
	case StatusEnviado, StatusSemEstoque, StatusPagamentoRecusado, StatusCancelado:
		return true
	default:
		return false
	}
}

// Cancelado diz se o pedido terminou sem ser entregue.
func (s Status) Cancelado() bool {
	switch s {
	case StatusSemEstoque, StatusPagamentoRecusado, StatusCancelado:
		return true
	default:
		return false
	}
}

// Erros do Registro.
var (
	ErrPedidoNaoEncontrado = errors.New("pedido não encontrado")
	ErrPedidoEncerrado     = errors.New("o pedido já foi encerrado")
)

// Marca é uma linha do histórico do pedido.
type Marca struct {
	Quando  time.Time `json:"quando"`
	Status  Status    `json:"status"`
	Detalhe string    `json:"detalhe"`
}

// Pedido é a visão do Principal sobre um pedido, montada a partir dos eventos
// que chegam. É também o formato devolvido ao frontend.
type Pedido struct {
	ID          string              `json:"id"`
	ClienteID   string              `json:"cliente_id"`
	Cliente     string              `json:"cliente"`
	Itens       []evento.ItemPedido `json:"itens"`
	Total       float64             `json:"total"`
	Status      Status              `json:"status"`
	Detalhe     string              `json:"detalhe"`
	URLCheckout string              `json:"url_checkout,omitempty"`
	CriadoEm    time.Time           `json:"criado_em"`
	Historico   []Marca             `json:"historico"`
}

// Registro guarda os pedidos em memória e é seguro para uso concorrente.
type Registro struct {
	mu      sync.RWMutex
	pedidos map[string]*Pedido
	ordem   []string
}

// NovoRegistro cria um registro vazio.
func NovoRegistro() *Registro {
	return &Registro{pedidos: make(map[string]*Pedido)}
}

// Criar registra um pedido novo do cliente e devolve uma cópia dele.
func (r *Registro) Criar(clienteID, cliente string, itens []evento.ItemPedido) Pedido {
	r.mu.Lock()
	defer r.mu.Unlock()

	agora := time.Now()
	pedido := &Pedido{
		ID:        novoIDPedido(),
		ClienteID: clienteID,
		Cliente:   cliente,
		Itens:     itens,
		Total:     totalDe(itens),
		Status:    StatusAguardandoEstoque,
		Detalhe:   "pedido criado",
		CriadoEm:  agora,
		Historico: []Marca{
			{Quando: agora, Status: StatusAguardandoEstoque, Detalhe: "pedido criado"},
		},
	}

	r.pedidos[pedido.ID] = pedido
	r.ordem = append(r.ordem, pedido.ID)
	return *pedido
}

// Atualizar move o pedido para um novo status e guarda o histórico. Devolve
// ErrPedidoNaoEncontrado se o pedido não existe e ErrPedidoEncerrado se ele
// já chegou ao fim: um evento atrasado não reabre um pedido encerrado.
func (r *Registro) Atualizar(pedidoID string, status Status, detalhe string) (Pedido, error) {
	return r.mudar(pedidoID, status, detalhe, "")
}

// AguardarPagamento guarda a URL de checkout e move o pedido para
// aguardando_pagamento, com as mesmas regras de Atualizar.
func (r *Registro) AguardarPagamento(pedidoID, urlCheckout, detalhe string) (Pedido, error) {
	return r.mudar(pedidoID, StatusAguardandoPagamento, detalhe, urlCheckout)
}

func (r *Registro) mudar(pedidoID string, status Status, detalhe, urlCheckout string) (Pedido, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pedido, existe := r.pedidos[pedidoID]
	switch {
	case !existe:
		return Pedido{}, ErrPedidoNaoEncontrado
	case pedido.Status.Encerrado():
		return *pedido, ErrPedidoEncerrado
	}

	if urlCheckout != "" {
		pedido.URLCheckout = urlCheckout
	}
	pedido.Status = status
	pedido.Detalhe = detalhe
	pedido.Historico = append(pedido.Historico, Marca{
		Quando:  time.Now(),
		Status:  status,
		Detalhe: detalhe,
	})
	return *pedido, nil
}

// Buscar devolve uma cópia do pedido.
func (r *Registro) Buscar(pedidoID string) (Pedido, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pedido, existe := r.pedidos[pedidoID]
	if !existe {
		return Pedido{}, false
	}
	return *pedido, true
}

// Listar devolve cópias de todos os pedidos, na ordem em que foram criados.
func (r *Registro) Listar() []Pedido {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lista := make([]Pedido, 0, len(r.ordem))
	for _, id := range r.ordem {
		if pedido, existe := r.pedidos[id]; existe {
			lista = append(lista, *pedido)
		}
	}
	return lista
}

// DoCliente devolve cópias dos pedidos de um cliente, na ordem em que foram
// criados.
func (r *Registro) DoCliente(clienteID string) []Pedido {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lista := []Pedido{}
	for _, id := range r.ordem {
		if pedido := r.pedidos[id]; pedido.ClienteID == clienteID {
			lista = append(lista, *pedido)
		}
	}
	return lista
}

// Quantidade devolve quantos pedidos existem.
func (r *Registro) Quantidade() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.ordem)
}

func totalDe(itens []evento.ItemPedido) float64 {
	var total float64
	for _, item := range itens {
		total += item.PrecoUnit * float64(item.Quantidade)
	}
	return total
}

// novoIDPedido gera um identificador curto e aleatório.
func novoIDPedido() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("PED-%06d", time.Now().UnixNano()%1000000)
	}
	return "PED-" + hex.EncodeToString(b)
}
