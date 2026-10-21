// Comando pagamento sobe o microsserviço de pagamentos.
//
//	go run ./cmd/pagamento
//
// Consome pedido.estoque_ok e pedido.excluido da exchange eCommerce. Cria a
// cobrança no Mock de Pagamento (MOCK_PAGAMENTO_URL), publica
// pagamento.pendente e recebe o resultado em POST /webhook/pagamento, na
// porta PAGAMENTO_PORTA, publicando pagamento.aprovado ou pagamento.recusado.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"ecommerce/internal/cripto"
	"ecommerce/internal/evento"
	"ecommerce/internal/mensageria"
	"ecommerce/internal/pagamento"
	"ecommerce/internal/rest"
)

const (
	portaPadrao      = "8082"
	urlMockPadrao    = "http://localhost:8090"
	urlWebhookPadrao = "http://localhost:8082/webhook/pagamento"
)

func main() {
	log.SetPrefix("[pagamento] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
	log.Println("encerrado")
}

func executar(ctx context.Context) error {
	chaveiro, err := cripto.CarregarChaveiro(mensageria.DiretorioChaves(), evento.ServicoPagamento)
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

	provedor := pagamento.NovoClienteProvedor(
		rest.Ambiente("MOCK_PAGAMENTO_URL", urlMockPadrao),
		rest.Ambiente("PAGAMENTO_WEBHOOK_URL", urlWebhookPadrao),
	)
	publicador := mensageria.NovoPublicador(conexao, chaveiro)
	servico := pagamento.NovoServico(provedor, publicador)

	consumidor, err := mensageria.NovoConsumidor(conexao, evento.FilaPagamento, chaveiro)
	if err != nil {
		return err
	}

	err = consumidor.Vincular(evento.ExchangeECommerce,
		evento.PedidoEstoqueOK,
		evento.PedidoExcluido,
	)
	if err != nil {
		return err
	}

	servidor := &http.Server{
		Addr:    rest.Endereco("PAGAMENTO_PORTA", portaPadrao),
		Handler: pagamento.NovoHandler(servico),
	}

	return rest.Rodar(ctx, servidor, func(ctx context.Context) error {
		return consumidor.Consumir(ctx, servico.Tratar)
	})
}
