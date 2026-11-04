package pagamento

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookHTTP(t *testing.T) {
	s, _, _ := montar(t)
	tratar(t, s, estoqueOK(t, "PED-20", 4500))

	casos := []struct {
		nome   string
		corpo  string
		status int
	}{
		{"aprovado", `{"cobranca_id":"COB-PED-20","pedido_id":"PED-20","status":"APROVADO"}`, http.StatusOK},
		{"aviso repetido", `{"cobranca_id":"COB-PED-20","pedido_id":"PED-20","status":"RECUSADO"}`, http.StatusOK},
		{"cobrança desconhecida", `{"cobranca_id":"COB-NADA","pedido_id":"PED-20","status":"APROVADO"}`, http.StatusNotFound},
		{"status inválido", `{"cobranca_id":"COB-PED-20","pedido_id":"PED-20","status":"TALVEZ"}`, http.StatusBadRequest},
		{"corpo que não é JSON", `nada`, http.StatusBadRequest},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			resposta := httptest.NewRecorder()
			requisicao := httptest.NewRequest(http.MethodPost, "/webhook/pagamento", strings.NewReader(c.corpo))

			NovoHandler(s).ServeHTTP(resposta, requisicao)

			if resposta.Code != c.status {
				t.Fatalf("esperava %d, veio %d: %s", c.status, resposta.Code, resposta.Body.String())
			}
		})
	}
}
