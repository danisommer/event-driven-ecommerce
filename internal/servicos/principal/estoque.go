package principal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tempoLimiteEstoque = 3 * time.Second

// Produto é um produto como o MS Estoque o descreve. Preco já é o que vale
// agora; PrecoOriginal só vem preenchido quando há promoção.
type Produto struct {
	ID            string  `json:"id"`
	Nome          string  `json:"nome"`
	Categoria     string  `json:"categoria"`
	Preco         float64 `json:"preco"`
	PrecoOriginal float64 `json:"preco_original,omitempty"`
	Quantidade    int     `json:"quantidade"`
}

// ClienteEstoque consulta o MS Estoque por HTTP.
type ClienteEstoque struct {
	urlBase string
	cliente *http.Client
}

// NovoClienteEstoque aponta para o MS Estoque em urlBase.
func NovoClienteEstoque(urlBase string) *ClienteEstoque {
	return &ClienteEstoque{
		urlBase: strings.TrimRight(urlBase, "/"),
		cliente: &http.Client{Timeout: tempoLimiteEstoque},
	}
}

// Disponiveis lista os produtos com saldo, via GET /produtos.
func (c *ClienteEstoque) Disponiveis(ctx context.Context) ([]Produto, error) {
	var produtos []Produto
	status, err := c.obter(ctx, "/produtos", &produtos)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("o Estoque respondeu %d em GET /produtos", status)
	}
	return produtos, nil
}

// Produto busca um produto com qualquer saldo, via GET /produtos/{id}. O
// segundo retorno é falso quando o Estoque responde 404.
func (c *ClienteEstoque) Produto(ctx context.Context, id string) (Produto, bool, error) {
	var produto Produto
	status, err := c.obter(ctx, "/produtos/"+url.PathEscape(id), &produto)

	switch {
	case err != nil:
		return Produto{}, false, err
	case status == http.StatusNotFound:
		return Produto{}, false, nil
	case status != http.StatusOK:
		return Produto{}, false, fmt.Errorf("o Estoque respondeu %d em GET /produtos/%s", status, id)
	default:
		return produto, true, nil
	}
}

// obter faz o GET e, com resposta 200, decodifica o JSON em destino.
func (c *ClienteEstoque) obter(ctx context.Context, caminho string, destino any) (int, error) {
	requisicao, err := http.NewRequestWithContext(ctx, http.MethodGet, c.urlBase+caminho, nil)
	if err != nil {
		return 0, fmt.Errorf("montando requisição ao Estoque: %w", err)
	}

	resposta, err := c.cliente.Do(requisicao)
	if err != nil {
		return 0, fmt.Errorf("chamando o Estoque em %s: %w", c.urlBase, err)
	}
	defer resposta.Body.Close()

	if resposta.StatusCode != http.StatusOK {
		return resposta.StatusCode, nil
	}
	if err := json.NewDecoder(resposta.Body).Decode(destino); err != nil {
		return 0, fmt.Errorf("lendo resposta do Estoque: %w", err)
	}
	return resposta.StatusCode, nil
}
