package pagamento

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ecommerce/internal/rest"
)

const tempoLimiteProvedor = 5 * time.Second

// CobrancaCriada é a resposta do provedor ao criar uma cobrança.
type CobrancaCriada struct {
	ID          string `json:"cobranca_id"`
	URLCheckout string `json:"url_checkout"`
}

// ClienteProvedor fala por HTTP com o provedor de pagamentos externo (o Mock).
type ClienteProvedor struct {
	urlBase    string
	webhookURL string
	cliente    *http.Client
}

// NovoClienteProvedor aponta para o provedor em urlBase. webhookURL é o
// endereço do webhook deste serviço, enviado em cada cobrança para o provedor
// avisar o resultado.
func NovoClienteProvedor(urlBase, webhookURL string) *ClienteProvedor {
	return &ClienteProvedor{
		urlBase:    strings.TrimRight(urlBase, "/"),
		webhookURL: webhookURL,
		cliente:    &http.Client{Timeout: tempoLimiteProvedor},
	}
}

type pedidoDeCobranca struct {
	PedidoID   string  `json:"pedido_id"`
	Valor      float64 `json:"valor"`
	WebhookURL string  `json:"webhook_url"`
}

// CriarCobranca pede ao provedor uma cobrança para o pedido. Devolve erro se o
// provedor não responder a tempo, recusar o pedido ou responder algo
// incompleto.
func (c *ClienteProvedor) CriarCobranca(ctx context.Context, pedidoID string, valor float64) (CobrancaCriada, error) {
	corpo, err := json.Marshal(pedidoDeCobranca{PedidoID: pedidoID, Valor: valor, WebhookURL: c.webhookURL})
	if err != nil {
		return CobrancaCriada{}, fmt.Errorf("serializando cobrança: %w", err)
	}

	requisicao, err := http.NewRequestWithContext(ctx, http.MethodPost, c.urlBase+"/cobrancas", bytes.NewReader(corpo))
	if err != nil {
		return CobrancaCriada{}, fmt.Errorf("montando requisição ao provedor: %w", err)
	}
	requisicao.Header.Set("Content-Type", "application/json")

	resposta, err := c.cliente.Do(requisicao)
	if err != nil {
		return CobrancaCriada{}, fmt.Errorf("chamando o provedor em %s: %w", c.urlBase, err)
	}
	defer resposta.Body.Close()

	if resposta.StatusCode != http.StatusCreated {
		var erro rest.Erro
		_ = json.NewDecoder(resposta.Body).Decode(&erro)
		return CobrancaCriada{}, fmt.Errorf("provedor respondeu %d: %s", resposta.StatusCode, erro.Erro)
	}

	var criada CobrancaCriada
	if err := json.NewDecoder(resposta.Body).Decode(&criada); err != nil {
		return CobrancaCriada{}, fmt.Errorf("lendo resposta do provedor: %w", err)
	}
	if criada.ID == "" || criada.URLCheckout == "" {
		return CobrancaCriada{}, errors.New("provedor respondeu sem cobranca_id ou url_checkout")
	}
	return criada, nil
}
