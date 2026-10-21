// Comando principal sobe o microsserviço Principal, o API Gateway da loja.
//
//	go run ./cmd/principal
//
// Atende a API REST e o SSE do frontend na porta GATEWAY_PORTA, consulta os
// produtos no MS Estoque (ESTOQUE_URL) e conversa com o resto do sistema pela
// exchange eCommerce: publica pedido.criado, pedido.excluido,
// interesse.promocao e interesse.cancelado, e consome os eventos de status
// dos pedidos.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"ecommerce/internal/cripto"
	"ecommerce/internal/evento"
	"ecommerce/internal/mensageria"
	"ecommerce/internal/principal"
	"ecommerce/internal/rest"
)

const (
	portaPadrao      = "8080"
	urlEstoquePadrao = "http://localhost:8081"
	origensPadrao    = "http://localhost:5173,http://127.0.0.1:5173"
)

func main() {
	log.SetPrefix("[principal] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
	log.Println("encerrado")
}

func executar(ctx context.Context) error {
	chaveiro, err := cripto.CarregarChaveiro(mensageria.DiretorioChaves(), evento.ServicoPrincipal)
	if err != nil {
		return err
	}

	conexao, err := mensageria.Conectar(mensageria.URLBroker())
	if err != nil {
		return err
	}
	defer conexao.Fechar()

	if err := conexao.DeclararExchanges(); err != nil {
		return err
	}

	publicador := mensageria.NovoPublicador(conexao, chaveiro)
	hub := principal.NovoHub()
	estoque := principal.NovoClienteEstoque(rest.Ambiente("ESTOQUE_URL", urlEstoquePadrao))
	servico := principal.NovoServico(principal.NovoRegistro(), estoque, publicador, hub.Publicar)

	consumidor, err := mensageria.NovoConsumidor(conexao, evento.FilaPrincipal, chaveiro)
	if err != nil {
		return err
	}

	err = consumidor.Vincular(evento.ExchangeECommerce,
		evento.PedidoEstoqueOK,
		evento.EstoqueIndisponivel,
		evento.PagamentoPendente,
		evento.PagamentoAprovado,
		evento.PagamentoRecusado,
		evento.PedidoEnviado,
	)
	if err != nil {
		return err
	}

	servidor := &http.Server{
		Addr:    rest.Endereco("GATEWAY_PORTA", portaPadrao),
		Handler: principal.NovoHandler(servico, hub, origens()),
	}

	return rest.Rodar(ctx, servidor, func(ctx context.Context) error {
		return consumidor.Consumir(ctx, servico.Tratar)
	})
}

// origens lê FRONTEND_ORIGEM, uma lista separada por vírgulas das origens do
// frontend liberadas no CORS.
func origens() []string {
	var lista []string
	for _, origem := range strings.Split(rest.Ambiente("FRONTEND_ORIGEM", origensPadrao), ",") {
		if origem = strings.TrimSpace(origem); origem != "" {
			lista = append(lista, origem)
		}
	}
	return lista
}
