package estoque

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ecommerce/internal/catalogo"
	"ecommerce/internal/rest"
)

func pedir(t *testing.T, s *Servico, caminho string) *httptest.ResponseRecorder {
	t.Helper()
	resposta := httptest.NewRecorder()
	NovoHandler(s).ServeHTTP(resposta, httptest.NewRequest(http.MethodGet, caminho, nil))
	return resposta
}

func decodificar(t *testing.T, resposta *httptest.ResponseRecorder, destino any) {
	t.Helper()
	if err := json.Unmarshal(resposta.Body.Bytes(), destino); err != nil {
		t.Fatalf("resposta não é JSON válido: %v\n%s", err, resposta.Body.String())
	}
}

func TestGetProdutosListaSoQuemTemSaldo(t *testing.T) {
	s, _ := montar(t)
	tratar(t, s, pedidoCriado(t, "PED-20", item("P006", 8)))

	resposta := pedir(t, s, "/produtos")
	if resposta.Code != http.StatusOK {
		t.Fatalf("esperava 200, veio %d", resposta.Code)
	}

	var produtos []Produto
	decodificar(t, resposta, &produtos)

	if len(produtos) != len(catalogo.Produtos)-1 {
		t.Fatalf("esperava %d produtos, vieram %d", len(catalogo.Produtos)-1, len(produtos))
	}
	for _, p := range produtos {
		if p.ID == "P006" {
			t.Fatal("o P006 está sem saldo e não deveria aparecer na lista")
		}
	}
}

func TestGetProdutoDevolveMesmoSemSaldo(t *testing.T) {
	s, _ := montar(t)
	tratar(t, s, pedidoCriado(t, "PED-21", item("P006", 8)))

	resposta := pedir(t, s, "/produtos/P006")
	if resposta.Code != http.StatusOK {
		t.Fatalf("esperava 200, veio %d", resposta.Code)
	}

	var produto Produto
	decodificar(t, resposta, &produto)

	if produto.ID != "P006" || produto.Quantidade != 0 || produto.Preco != 450 {
		t.Fatalf("produto inesperado: %+v", produto)
	}
}

func TestGetProdutosMostraOPrecoPromocional(t *testing.T) {
	s, _ := montar(t)
	tratar(t, s, promocaoCriada(t, "P005", 167.2, time.Now().Add(time.Hour)))

	var produtos []Produto
	decodificar(t, pedir(t, s, "/produtos"), &produtos)

	for _, p := range produtos {
		if p.ID == "P005" {
			if p.Preco != 167.2 || p.PrecoOriginal != 190 {
				t.Fatalf("P005 deveria sair com o preço promocional: %+v", p)
			}
			return
		}
	}
	t.Fatal("P005 não apareceu na lista")
}

func TestGetProdutoInexistenteDa404(t *testing.T) {
	s, _ := montar(t)

	resposta := pedir(t, s, "/produtos/P999")
	if resposta.Code != http.StatusNotFound {
		t.Fatalf("esperava 404, veio %d", resposta.Code)
	}

	var erro rest.Erro
	decodificar(t, resposta, &erro)
	if erro.Erro == "" {
		t.Fatal("a resposta de erro veio sem mensagem")
	}
}
