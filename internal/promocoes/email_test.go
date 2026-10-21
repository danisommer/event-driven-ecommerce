package promocoes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendEnviaComAChaveEORemetente(t *testing.T) {
	var recebido emailResend
	var autorizacao string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		autorizacao = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&recebido); err != nil {
			t.Errorf("corpo inválido: %v", err)
		}
		w.Write([]byte(`{"id":"email-1"}`))
	}))
	defer api.Close()

	resend := NovoResend("re_teste", "Loja <onboarding@resend.dev>")
	resend.url = api.URL

	if err := resend.Enviar(context.Background(), "ana@exemplo.com", "Promoção", "<p>oi</p>"); err != nil {
		t.Fatalf("enviando: %v", err)
	}

	if autorizacao != "Bearer re_teste" {
		t.Fatalf("cabeçalho Authorization inesperado: %q", autorizacao)
	}
	if recebido.From != "Loja <onboarding@resend.dev>" || len(recebido.To) != 1 || recebido.To[0] != "ana@exemplo.com" ||
		recebido.Subject != "Promoção" || recebido.HTML != "<p>oi</p>" {
		t.Fatalf("a Resend recebeu %+v", recebido)
	}
}

func TestResendRecusadoDevolveOErroDaAPI(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"You can only send testing emails to your own email address"}`))
	}))
	defer api.Close()

	resend := NovoResend("re_teste", "Loja <onboarding@resend.dev>")
	resend.url = api.URL

	err := resend.Enviar(context.Background(), "outra@exemplo.com", "Promoção", "<p>oi</p>")

	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "own email address") {
		t.Fatalf("esperava erro 403 com a mensagem da API, veio %v", err)
	}
}
