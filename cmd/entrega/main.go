// Comando entrega sobe o microsserviço de emissão de nota e expedição.
//
//	go run ./cmd/entrega
//
// Consome pagamento.aprovado da exchange eCommerce e publica pedido.enviado.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ecommerce/internal/comum/cripto"
	"ecommerce/internal/comum/evento"
	"ecommerce/internal/comum/mensageria"
	"ecommerce/internal/servicos/entrega"
)

func main() {
	log.SetPrefix("[entrega] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
	log.Println("encerrado")
}

func executar(ctx context.Context) error {
	chaveiro, err := cripto.CarregarChaveiro(mensageria.DiretorioChaves(), evento.ServicoEntrega)
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
	servico := entrega.NovoServico(publicador)

	consumidor, err := mensageria.NovoConsumidor(conexao, evento.FilaEntrega, chaveiro)
	if err != nil {
		return err
	}

	if err := consumidor.Vincular(evento.ExchangeECommerce, evento.PagamentoAprovado); err != nil {
		return err
	}

	return consumidor.Consumir(ctx, servico.Tratar)
}
