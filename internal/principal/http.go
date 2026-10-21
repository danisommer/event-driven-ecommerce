package principal

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	"ecommerce/internal/rest"
)

// intervaloBatidaSSE é de quanto em quanto tempo a conexão SSE recebe um
// comentário, para não ser derrubada por inatividade.
const intervaloBatidaSSE = 15 * time.Second

// NovoHandler monta a API REST consumida pelo frontend, com CORS liberado
// para as origens informadas.
func NovoHandler(servico *Servico, hub *Hub, origens []string) http.Handler {
	a := api{servico: servico, hub: hub}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /produtos", a.listarProdutos)
	mux.HandleFunc("POST /pedidos", a.criarPedido)
	mux.HandleFunc("GET /pedidos", a.listarPedidos)
	mux.HandleFunc("DELETE /pedidos/{id}", a.cancelarPedido)
	mux.HandleFunc("GET /eventos", a.eventos)
	mux.HandleFunc("POST /interesses", a.registrarInteresse)
	mux.HandleFunc("DELETE /interesses/{email}", a.cancelarInteresse)
	return comCORS(origens, mux)
}

type api struct {
	servico *Servico
	hub     *Hub
}

type pedidoSolicitado struct {
	ClienteID string           `json:"cliente_id"`
	Cliente   string           `json:"cliente"`
	Itens     []ItemSolicitado `json:"itens"`
}

type interesseSolicitado struct {
	Email      string   `json:"email"`
	Categorias []string `json:"categorias"`
}

func (a api) listarProdutos(w http.ResponseWriter, r *http.Request) {
	produtos, err := a.servico.Produtos(r.Context())
	if err != nil {
		responderFalha(w, err)
		return
	}
	rest.ResponderJSON(w, http.StatusOK, produtos)
}

func (a api) criarPedido(w http.ResponseWriter, r *http.Request) {
	var corpo pedidoSolicitado
	if err := json.NewDecoder(r.Body).Decode(&corpo); err != nil {
		rest.ResponderErro(w, http.StatusBadRequest, "o corpo deve ser JSON com cliente_id, cliente e itens")
		return
	}

	pedido, err := a.servico.CriarPedido(r.Context(), corpo.ClienteID, corpo.Cliente, corpo.Itens)
	if err != nil {
		responderFalha(w, err)
		return
	}
	rest.ResponderJSON(w, http.StatusAccepted, pedido)
}

func (a api) listarPedidos(w http.ResponseWriter, r *http.Request) {
	clienteID := r.URL.Query().Get("cliente_id")
	if clienteID == "" {
		rest.ResponderErro(w, http.StatusBadRequest, "informe cliente_id")
		return
	}
	rest.ResponderJSON(w, http.StatusOK, a.servico.PedidosDoCliente(clienteID))
}

func (a api) cancelarPedido(w http.ResponseWriter, r *http.Request) {
	pedido, err := a.servico.CancelarPedido(r.Context(), r.PathValue("id"), r.URL.Query().Get("cliente_id"))
	if err != nil {
		responderFalha(w, err)
		return
	}
	rest.ResponderJSON(w, http.StatusAccepted, pedido)
}

func (a api) registrarInteresse(w http.ResponseWriter, r *http.Request) {
	var corpo interesseSolicitado
	if err := json.NewDecoder(r.Body).Decode(&corpo); err != nil {
		rest.ResponderErro(w, http.StatusBadRequest, "o corpo deve ser JSON com email e categorias")
		return
	}

	dados, err := a.servico.RegistrarInteresse(r.Context(), corpo.Email, corpo.Categorias)
	if err != nil {
		responderFalha(w, err)
		return
	}
	rest.ResponderJSON(w, http.StatusAccepted, dados)
}

func (a api) cancelarInteresse(w http.ResponseWriter, r *http.Request) {
	dados, err := a.servico.CancelarInteresse(r.Context(), r.PathValue("email"))
	if err != nil {
		responderFalha(w, err)
		return
	}
	rest.ResponderJSON(w, http.StatusAccepted, dados)
}

// eventos mantém a conexão SSE do cliente aberta e escreve um evento "pedido"
// a cada mudança de um pedido dele. Assina o hub antes de responder, para
// nenhuma mudança se perder entre a resposta e o início da escuta.
func (a api) eventos(w http.ResponseWriter, r *http.Request) {
	clienteID := r.URL.Query().Get("cliente_id")
	if clienteID == "" {
		rest.ResponderErro(w, http.StatusBadRequest, "informe cliente_id")
		return
	}

	mudancas, encerrar := a.hub.Assinar(clienteID)
	defer encerrar()

	controle := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := controle.Flush(); err != nil {
		log.Printf("SSE: a conexão não aceita streaming: %v", err)
		return
	}

	log.Printf("SSE: cliente %s conectado (%d conexão(ões) abertas)", clienteID, a.hub.Conexoes(clienteID))
	defer log.Printf("SSE: cliente %s desconectado", clienteID)

	batida := time.NewTicker(intervaloBatidaSSE)
	defer batida.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case pedido, aberta := <-mudancas:
			if !aberta {
				return
			}
			if err := escreverEvento(w, controle, pedido); err != nil {
				return
			}
		case <-batida.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := controle.Flush(); err != nil {
				return
			}
		}
	}
}

func escreverEvento(w http.ResponseWriter, controle *http.ResponseController, pedido Pedido) error {
	dados, err := json.Marshal(pedido)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: pedido\ndata: %s\n\n", dados); err != nil {
		return err
	}
	return controle.Flush()
}

// responderFalha traduz os erros do serviço em status HTTP. Detalhes de
// falhas de infraestrutura vão para o log, não para o cliente.
func responderFalha(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPedidoInvalido), errors.Is(err, ErrInteresseInvalido):
		rest.ResponderErro(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrPedidoNaoEncontrado):
		rest.ResponderErro(w, http.StatusNotFound, ErrPedidoNaoEncontrado.Error())
	case errors.Is(err, ErrPedidoEncerrado):
		rest.ResponderErro(w, http.StatusConflict, ErrPedidoEncerrado.Error())
	case errors.Is(err, ErrEstoqueIndisponivel):
		log.Printf("%v", err)
		rest.ResponderErro(w, http.StatusBadGateway, ErrEstoqueIndisponivel.Error())
	case errors.Is(err, ErrBrokerIndisponivel):
		log.Printf("%v", err)
		rest.ResponderErro(w, http.StatusServiceUnavailable, ErrBrokerIndisponivel.Error())
	default:
		log.Printf("erro inesperado: %v", err)
		rest.ResponderErro(w, http.StatusInternalServerError, "erro inesperado")
	}
}

// comCORS libera as origens do frontend: responde ao preflight (OPTIONS) que o
// navegador manda antes de um POST com JSON ou de um DELETE, e marca as
// respostas com Access-Control-Allow-Origin.
func comCORS(origens []string, proximo http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origem := r.Header.Get("Origin"); slices.Contains(origens, origem) {
			w.Header().Set("Access-Control-Allow-Origin", origem)
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		proximo.ServeHTTP(w, r)
	})
}
