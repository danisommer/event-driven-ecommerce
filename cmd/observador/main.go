// Comando observador é uma ferramenta de desenvolvimento: escuta todos os
// eventos da exchange eCommerce, numa fila própria, e imprime cada um.
//
//	go run ./cmd/observador
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ecommerce/internal/cripto"
	"ecommerce/internal/evento"
	"ecommerce/internal/mensageria"
)

const nomeFila = "dev.observador"

func main() {
	log.SetPrefix("[observador] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
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

	consumidor, err := mensageria.NovoConsumidor(conexao, nomeFila, chaveiro)
	if err != nil {
		return err
	}

	err = consumidor.Vincular(evento.ExchangeECommerce,
		evento.PedidoCriado,
		evento.PedidoExcluido,
		evento.PedidoEstoqueOK,
		evento.EstoqueIndisponivel,
		evento.PagamentoPendente,
		evento.PagamentoAprovado,
		evento.PagamentoRecusado,
		evento.PedidoEnviado,
		evento.InteressePromocao,
		evento.InteresseCancelado,
		evento.PromocaoCriada,
	)
	if err != nil {
		return err
	}

	return consumidor.Consumir(ctx, imprimir)
}

func imprimir(_ context.Context, env evento.Envelope) error {
	dados, err := json.MarshalIndent(json.RawMessage(env.Dados), "    ", "  ")
	if err != nil {
		dados = env.Dados
	}
	log.Printf("%s de %s\n    %s", env.Tipo, env.Produtor, dados)
	return nil
}
