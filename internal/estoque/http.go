package estoque

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"ecommerce/internal/rest"
)

// Consulta é o que a API HTTP precisa do serviço para responder.
type Consulta interface {
	ProdutosDisponiveis(ctx context.Context) ([]Produto, error)
	Produto(ctx context.Context, id string) (Produto, bool, error)
}

// NovoHandler monta as rotas HTTP do Estoque, consumidas pelo Gateway:
// GET /produtos lista os produtos com saldo e GET /produtos/{id} devolve um
// produto com qualquer saldo.
func NovoHandler(consulta Consulta) http.Handler {
	a := api{consulta: consulta}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /produtos", a.listar)
	mux.HandleFunc("GET /produtos/{id}", a.buscar)
	return mux
}

type api struct {
	consulta Consulta
}

func (a api) listar(w http.ResponseWriter, r *http.Request) {
	produtos, err := a.consulta.ProdutosDisponiveis(r.Context())
	if err != nil {
		log.Printf("GET /produtos: %v", err)
		rest.ResponderErro(w, http.StatusInternalServerError, "não consegui consultar o estoque")
		return
	}

	log.Printf("GET /produtos: %d produto(s) com saldo", len(produtos))
	rest.ResponderJSON(w, http.StatusOK, produtos)
}

func (a api) buscar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	produto, existe, err := a.consulta.Produto(r.Context(), id)

	switch {
	case err != nil:
		log.Printf("GET /produtos/%s: %v", id, err)
		rest.ResponderErro(w, http.StatusInternalServerError, "não consegui consultar o estoque")
	case !existe:
		rest.ResponderErro(w, http.StatusNotFound, fmt.Sprintf("produto %s não existe", id))
	default:
		log.Printf("GET /produtos/%s: saldo %d", id, produto.Quantidade)
		rest.ResponderJSON(w, http.StatusOK, produto)
	}
}
