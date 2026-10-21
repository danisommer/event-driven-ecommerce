package principal

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ecommerce/internal/evento"
)

const origemTeste = "http://localhost:5173"

func montarAPI(t *testing.T) (http.Handler, *Servico, *publicadorFalso) {
	t.Helper()
	pub := &publicadorFalso{}
	hub := NovoHub()
	servico := NovoServico(NovoRegistro(), &estoqueFalso{}, pub, hub.Publicar)
	return NovoHandler(servico, hub, []string{origemTeste}), servico, pub
}

func requisitar(handler http.Handler, metodo, caminho, corpo string) *httptest.ResponseRecorder {
	resposta := httptest.NewRecorder()
	handler.ServeHTTP(resposta, httptest.NewRequest(metodo, caminho, strings.NewReader(corpo)))
	return resposta
}

func TestPostPedidosResponde202ComOPedido(t *testing.T) {
	handler, _, pub := montarAPI(t)

	resposta := requisitar(handler, http.MethodPost, "/pedidos",
		`{"cliente_id":"cliente-1","cliente":"Daniel","itens":[{"produto_id":"P001","quantidade":1}]}`)

	if resposta.Code != http.StatusAccepted {
		t.Fatalf("esperava 202, veio %d: %s", resposta.Code, resposta.Body.String())
	}
	var pedido Pedido
	if err := json.Unmarshal(resposta.Body.Bytes(), &pedido); err != nil {
		t.Fatalf("resposta não é um pedido: %v", err)
	}
	if pedido.Status != StatusAguardandoEstoque || pedido.Total != 4500 || pedido.ClienteID != "cliente-1" {
		t.Fatalf("pedido inesperado: %+v", pedido)
	}
	if len(pub.publicados) != 1 {
		t.Fatalf("esperava publicar pedido.criado, publicou %v", pub.chaves())
	}
}

func TestRespostasDeErroDaAPI(t *testing.T) {
	handler, servico, _ := montarAPI(t)

	enviado, err := servico.CriarPedido(context.Background(), "cliente-1", "Daniel",
		[]ItemSolicitado{{ProdutoID: "P001", Quantidade: 1}})
	if err != nil {
		t.Fatalf("criando pedido: %v", err)
	}
	entregar(t, servico, evento.PedidoEnviado, evento.ServicoEntrega, evento.DadosPedidoEnviado{PedidoID: enviado.ID})

	casos := []struct {
		nome    string
		metodo  string
		caminho string
		corpo   string
		status  int
	}{
		{"pedido com corpo que não é JSON", http.MethodPost, "/pedidos", "nada", http.StatusBadRequest},
		{"pedido com produto inexistente", http.MethodPost, "/pedidos",
			`{"cliente_id":"cliente-1","itens":[{"produto_id":"P999","quantidade":1}]}`, http.StatusBadRequest},
		{"listar pedidos sem cliente_id", http.MethodGet, "/pedidos", "", http.StatusBadRequest},
		{"cancelar pedido de outro cliente", http.MethodDelete, "/pedidos/" + enviado.ID + "?cliente_id=cliente-2", "", http.StatusNotFound},
		{"cancelar pedido já enviado", http.MethodDelete, "/pedidos/" + enviado.ID + "?cliente_id=cliente-1", "", http.StatusConflict},
		{"interesse com e-mail inválido", http.MethodPost, "/interesses", `{"email":"fulano","categorias":["A"]}`, http.StatusBadRequest},
		{"interesse válido", http.MethodPost, "/interesses", `{"email":"fulano@exemplo.com","categorias":["A"]}`, http.StatusAccepted},
		{"cancelar interesse", http.MethodDelete, "/interesses/fulano@exemplo.com", "", http.StatusAccepted},
		{"eventos sem cliente_id", http.MethodGet, "/eventos", "", http.StatusBadRequest},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			resposta := requisitar(handler, c.metodo, c.caminho, c.corpo)
			if resposta.Code != c.status {
				t.Fatalf("esperava %d, veio %d: %s", c.status, resposta.Code, resposta.Body.String())
			}
		})
	}
}

func TestCORSLiberaSoAOrigemDoFrontend(t *testing.T) {
	handler, _, _ := montarAPI(t)

	preflight := httptest.NewRequest(http.MethodOptions, "/pedidos", nil)
	preflight.Header.Set("Origin", origemTeste)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	resposta := httptest.NewRecorder()
	handler.ServeHTTP(resposta, preflight)

	if resposta.Code != http.StatusNoContent || resposta.Header().Get("Access-Control-Allow-Origin") != origemTeste {
		t.Fatalf("preflight do frontend recusado: %d %v", resposta.Code, resposta.Header())
	}

	estranha := httptest.NewRequest(http.MethodGet, "/pedidos?cliente_id=cliente-1", nil)
	estranha.Header.Set("Origin", "http://site-estranho.teste")
	resposta = httptest.NewRecorder()
	handler.ServeHTTP(resposta, estranha)

	if origem := resposta.Header().Get("Access-Control-Allow-Origin"); origem != "" {
		t.Fatalf("origem estranha não deveria ser liberada, veio %q", origem)
	}
}

func TestSSEEntregaAMudancaDoPedidoAoDono(t *testing.T) {
	handler, servico, _ := montarAPI(t)
	servidor := httptest.NewServer(handler)
	defer servidor.Close()

	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()

	requisicao, err := http.NewRequestWithContext(ctx, http.MethodGet, servidor.URL+"/eventos?cliente_id=cliente-1", nil)
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}
	resposta, err := http.DefaultClient.Do(requisicao)
	if err != nil {
		t.Fatalf("abrindo o SSE: %v", err)
	}
	defer resposta.Body.Close()

	if tipo := resposta.Header.Get("Content-Type"); tipo != "text/event-stream" {
		t.Fatalf("Content-Type inesperado: %q", tipo)
	}

	criado, err := servico.CriarPedido(context.Background(), "cliente-1", "Daniel",
		[]ItemSolicitado{{ProdutoID: "P001", Quantidade: 1}})
	if err != nil {
		t.Fatalf("criando pedido: %v", err)
	}

	var nomeEvento, dados string
	linhas := bufio.NewScanner(resposta.Body)
	for dados == "" && linhas.Scan() {
		linha := linhas.Text()
		switch {
		case strings.HasPrefix(linha, "event: "):
			nomeEvento = strings.TrimPrefix(linha, "event: ")
		case strings.HasPrefix(linha, "data: "):
			dados = strings.TrimPrefix(linha, "data: ")
		}
	}

	if nomeEvento != "pedido" {
		t.Fatalf("esperava o evento \"pedido\", veio %q", nomeEvento)
	}
	var recebido Pedido
	if err := json.Unmarshal([]byte(dados), &recebido); err != nil {
		t.Fatalf("data do SSE não é um pedido: %v", err)
	}
	if recebido.ID != criado.ID || recebido.Status != StatusAguardandoEstoque {
		t.Fatalf("pedido inesperado no SSE: %+v", recebido)
	}
}
