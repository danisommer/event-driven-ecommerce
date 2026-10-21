// Comando estoque sobe o microsserviço de gerenciamento de estoque.
//
//	go run ./cmd/estoque
//
// Consome pedido.criado e pedido.excluido da exchange eCommerce e publica
// pedido.estoque_ok ou estoque.indisponivel. Consome também promocao.criada.
// Guarda saldo e promoções no SQLite (ESTOQUE_BANCO) e atende GET /produtos na
// porta ESTOQUE_PORTA.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"ecommerce/internal/cripto"
	"ecommerce/internal/estoque"
	"ecommerce/internal/evento"
	"ecommerce/internal/mensageria"
	"ecommerce/internal/rest"
)

const (
	portaPadrao = "8081"
	bancoPadrao = "estoque.db"
)

func main() {
	log.SetPrefix("[estoque] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
	log.Println("encerrado")
}

func executar(ctx context.Context) error {
	chaveiro, err := cripto.CarregarChaveiro(mensageria.DiretorioChaves(), evento.ServicoEstoque)
	if err != nil {
		return err
	}

	repositorio, err := estoque.AbrirRepositorio(rest.Ambiente("ESTOQUE_BANCO", bancoPadrao))
	if err != nil {
		return err
	}
	defer repositorio.Fechar()

	conexao, err := mensageria.Conectar(mensageria.URLBroker())
	if err != nil {
		return err
	}
	defer conexao.Fechar()

	if err := conexao.DeclararExchanges(); err != nil {
		return err
	}

	publicador := mensageria.NovoPublicador(conexao, chaveiro)
	servico := estoque.NovoServico(repositorio, publicador)

	consumidor, err := mensageria.NovoConsumidor(conexao, evento.FilaEstoque, chaveiro)
	if err != nil {
		return err
	}

	err = consumidor.Vincular(evento.ExchangeECommerce,
		evento.PedidoCriado,
		evento.PedidoExcluido,
		evento.PromocaoCriada,
	)
	if err != nil {
		return err
	}

	servidor := &http.Server{
		Addr:    rest.Endereco("ESTOQUE_PORTA", portaPadrao),
		Handler: estoque.NovoHandler(servico),
	}

	return rest.Rodar(ctx, servidor, func(ctx context.Context) error {
		return consumidor.Consumir(ctx, servico.Tratar)
	})
}
