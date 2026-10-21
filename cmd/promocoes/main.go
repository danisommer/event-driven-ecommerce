// Comando promocoes sobe o microsserviço de promoções.
//
//	go run ./cmd/promocoes
//
// Consome interesse.promocao e interesse.cancelado da exchange eCommerce e
// guarda as inscrições no SQLite (PROMOCOES_BANCO). A cada
// PROMOCOES_INTERVALO gera uma promoção, publica promocao.criada e envia os
// e-mails pela Resend (RESEND_API_KEY, EMAIL_REMETENTE); sem a chave, só
// registra os e-mails no log.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ecommerce/internal/cripto"
	"ecommerce/internal/evento"
	"ecommerce/internal/mensageria"
	"ecommerce/internal/promocoes"
	"ecommerce/internal/rest"
)

const (
	bancoPadrao     = "promocoes.db"
	intervaloPadrao = "2m"
	remetentePadrao = "Loja SD <onboarding@resend.dev>"
)

func main() {
	log.SetPrefix("[promocoes] ")
	log.SetFlags(log.Ltime)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	if err := executar(ctx); err != nil {
		log.Fatalf("erro: %v", err)
	}
	log.Println("encerrado")
}

func executar(ctx context.Context) error {
	intervalo, err := lerIntervalo()
	if err != nil {
		return err
	}

	chaveiro, err := cripto.CarregarChaveiro(mensageria.DiretorioChaves(), evento.ServicoPromocoes)
	if err != nil {
		return err
	}

	repositorio, err := promocoes.AbrirRepositorio(rest.Ambiente("PROMOCOES_BANCO", bancoPadrao))
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
	servico := promocoes.NovoServico(repositorio, notificador(), publicador, intervalo)

	consumidor, err := mensageria.NovoConsumidor(conexao, evento.FilaPromocoes, chaveiro)
	if err != nil {
		return err
	}

	err = consumidor.Vincular(evento.ExchangeECommerce,
		evento.InteressePromocao,
		evento.InteresseCancelado,
	)
	if err != nil {
		return err
	}

	erros := make(chan error, 2)
	go func() { erros <- consumidor.Consumir(ctx, servico.Tratar) }()
	go func() { erros <- servico.EnviarPromocoes(ctx) }()

	if err := <-erros; err != nil {
		return err
	}
	return <-erros
}

// lerIntervalo lê PROMOCOES_INTERVALO no formato de duração do Go (30s, 2m).
func lerIntervalo() (time.Duration, error) {
	bruto := rest.Ambiente("PROMOCOES_INTERVALO", intervaloPadrao)
	intervalo, err := time.ParseDuration(bruto)
	if err != nil || intervalo <= 0 {
		return 0, fmt.Errorf("PROMOCOES_INTERVALO=%q inválido: use algo como 30s ou 2m", bruto)
	}
	return intervalo, nil
}

// notificador usa a Resend quando RESEND_API_KEY está definida e, sem ela,
// só registra os e-mails no log.
func notificador() promocoes.Notificador {
	chave := os.Getenv("RESEND_API_KEY")
	if chave == "" {
		log.Printf("RESEND_API_KEY não definida: os e-mails serão só registrados no log")
		return promocoes.RegistroDeEmail{}
	}
	return promocoes.NovoResend(chave, rest.Ambiente("EMAIL_REMETENTE", remetentePadrao))
}
