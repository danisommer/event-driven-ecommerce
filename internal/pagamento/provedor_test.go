package pagamento

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const webhookTeste = "http://pagamento.teste/webhook/pagamento"

func TestClienteProvedorEnviaWebhookEDevolveCheckout(t *testing.T) {
	var recebido pedidoDeCobranca
	provedor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cobrancas" {
			t.Errorf("esperava POST /cobrancas, veio %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&recebido); err != nil {
			t.Errorf("corpo inválido: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"cobranca_id":"COB-1","url_checkout":"http://mock.teste/checkout/COB-1"}`))
	}))
	defer provedor.Close()

	criada, err := NovoClienteProvedor(provedor.URL, webhookTeste).CriarCobranca(context.Background(), "PED-1", 4500)
	if err != nil {
		t.Fatalf("criando cobrança: %v", err)
	}

	if criada.ID != "COB-1" || criada.URLCheckout != "http://mock.teste/checkout/COB-1" {
		t.Fatalf("resposta lida errado: %+v", criada)
	}
	if recebido.PedidoID != "PED-1" || recebido.Valor != 4500 || recebido.WebhookURL != webhookTeste {
		t.Fatalf("o provedor recebeu %+v", recebido)
	}
}

func TestClienteProvedorRecusadoDevolveErroComAMensagem(t *testing.T) {
	provedor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"erro":"valor deve ser um número positivo"}`))
	}))
	defer provedor.Close()

	_, err := NovoClienteProvedor(provedor.URL, webhookTeste).CriarCobranca(context.Background(), "PED-1", 0)

	if err == nil || !strings.Contains(err.Error(), "valor deve ser um número positivo") {
		t.Fatalf("esperava erro com a mensagem do provedor, veio %v", err)
	}
}

func TestClienteProvedorForaDoArDevolveErro(t *testing.T) {
	provedor := httptest.NewServer(http.NotFoundHandler())
	endereco := provedor.URL
	provedor.Close()

	if _, err := NovoClienteProvedor(endereco, webhookTeste).CriarCobranca(context.Background(), "PED-1", 4500); err == nil {
		t.Fatal("com o provedor fora do ar, CriarCobranca deveria devolver erro")
	}
}
