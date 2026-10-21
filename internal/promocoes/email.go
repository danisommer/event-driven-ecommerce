package promocoes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	urlResend         = "https://api.resend.com/emails"
	tempoLimiteResend = 10 * time.Second
)

// Resend envia e-mails pela API HTTP da Resend.
type Resend struct {
	url       string
	chave     string
	remetente string
	cliente   *http.Client
}

// NovoResend usa a chave da API e o remetente informados.
func NovoResend(chave, remetente string) *Resend {
	return &Resend{
		url:       urlResend,
		chave:     chave,
		remetente: remetente,
		cliente:   &http.Client{Timeout: tempoLimiteResend},
	}
}

type emailResend struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// Enviar faz POST /emails na Resend. Uma resposta fora de 2xx vira erro com o
// que a API devolveu.
func (r *Resend) Enviar(ctx context.Context, para, assunto, corpoHTML string) error {
	corpo, err := json.Marshal(emailResend{From: r.remetente, To: []string{para}, Subject: assunto, HTML: corpoHTML})
	if err != nil {
		return fmt.Errorf("serializando o e-mail: %w", err)
	}

	requisicao, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(corpo))
	if err != nil {
		return fmt.Errorf("montando a requisição à Resend: %w", err)
	}
	requisicao.Header.Set("Authorization", "Bearer "+r.chave)
	requisicao.Header.Set("Content-Type", "application/json")

	resposta, err := r.cliente.Do(requisicao)
	if err != nil {
		return fmt.Errorf("chamando a Resend: %w", err)
	}
	defer resposta.Body.Close()

	if resposta.StatusCode/100 != 2 {
		detalhe, _ := io.ReadAll(io.LimitReader(resposta.Body, 512))
		return fmt.Errorf("a Resend respondeu %d: %s", resposta.StatusCode, strings.TrimSpace(string(detalhe)))
	}
	return nil
}

// RegistroDeEmail só registra no log o e-mail que seria enviado, para rodar o
// serviço sem a chave da Resend.
type RegistroDeEmail struct{}

// Enviar registra destinatário e assunto no log.
func (RegistroDeEmail) Enviar(_ context.Context, para, assunto, _ string) error {
	log.Printf("[e-mail não enviado: sem RESEND_API_KEY] para %s: %s", para, assunto)
	return nil
}
