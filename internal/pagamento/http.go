package pagamento

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"ecommerce/internal/rest"
)

// Decisor é o que a API HTTP precisa do serviço para receber o webhook.
type Decisor interface {
	Decidir(ctx context.Context, cobrancaID, pedidoID, decisao string) error
}

// NovoHandler monta a rota do webhook chamada pelo provedor de pagamentos:
// POST /webhook/pagamento com {cobranca_id, pedido_id, status}.
func NovoHandler(decisor Decisor) http.Handler {
	a := api{decisor: decisor}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/pagamento", a.receberWebhook)
	return mux
}

type api struct {
	decisor Decisor
}

type avisoDoProvedor struct {
	CobrancaID string `json:"cobranca_id"`
	PedidoID   string `json:"pedido_id"`
	Status     string `json:"status"`
}

func (a api) receberWebhook(w http.ResponseWriter, r *http.Request) {
	var aviso avisoDoProvedor
	if err := json.NewDecoder(r.Body).Decode(&aviso); err != nil {
		rest.ResponderErro(w, http.StatusBadRequest, "o corpo deve ser JSON com cobranca_id, pedido_id e status")
		return
	}

	log.Printf("webhook: cobrança %s do pedido %s → %s", aviso.CobrancaID, aviso.PedidoID, aviso.Status)
	err := a.decisor.Decidir(r.Context(), aviso.CobrancaID, aviso.PedidoID, aviso.Status)

	switch {
	case errors.Is(err, ErrDecisaoInvalida), errors.Is(err, ErrPedidoDivergente):
		rest.ResponderErro(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrCobrancaDesconhecida):
		rest.ResponderErro(w, http.StatusNotFound, err.Error())
	case err != nil:
		log.Printf("webhook da cobrança %s: %v", aviso.CobrancaID, err)
		rest.ResponderErro(w, http.StatusInternalServerError, "não consegui registrar o pagamento")
	default:
		rest.ResponderJSON(w, http.StatusOK, map[string]bool{"recebido": true})
	}
}
