package entrega

import (
	"context"
	"ecommerce/internal/comum/evento"
	"fmt"
	"log"
	"maps"
	"math/rand/v2"
	"slices"
	"time"
)

const (
	TEMPO_PROCESSAMENTO_MIN_MS = 1500
	TEMPO_PROCESSAMENTO_MAX_MS = 4000
	CODIGO_RASTREIO_SUFIXO     = "BR"
)

var TRANSPORTADORAS_DISPONIVEIS = map[string]string{
	"Transportadora Capivara": "TC",
	"Leon Entregas":           "LE",
	"MaggieFast":              "MF",
	"Correios":                "CR",
}

type publicador interface {
	PublicarECommerce(ctx context.Context, routingKey string, dados any) error
}

type Servico struct {
	publicador publicador
}

func NovoServico(publicador publicador) *Servico {
	return &Servico{publicador}
}

func (s *Servico) Tratar(ctx context.Context, env evento.Envelope) error {
	if env.Tipo != evento.PagamentoAprovado {
		return fmt.Errorf("Evento \"%s\" não é suportado por este microserviço. Ignorando mensagem...", env.Tipo)
	}

	var payload evento.DadosPagamentoAprovado

	if err := env.DecodificarDados(&payload); err != nil {
		return fmt.Errorf("Falha ao lidar com evento de PagamentoAprovado. %w", err)
	}

	pedidoEnviadoDados := s.ProcessarEntrega(ctx, payload)

	if err := s.publicador.PublicarECommerce(ctx, evento.PedidoEnviado, pedidoEnviadoDados); err != nil {
		return fmt.Errorf("Falha ao lidar com evento de PagamentoAprovado. Não foi possível publicar a mensagem no broker. %w", err)
	}

	return nil
}

func (s *Servico) ProcessarEntrega(ctx context.Context, payload evento.DadosPagamentoAprovado) *evento.DadosPedidoEnviado {
	s.simularProcessamento(ctx, payload)

	notaFiscal := s.obterNotaFiscal()
	transportadora, codigoRastreio := s.obterTransportadoraECodigoRastreio()

	return &evento.DadosPedidoEnviado{
		PedidoID:       payload.PedidoID,
		NotaFiscal:     notaFiscal,
		Transportadora: transportadora,
		CodigoRastreio: codigoRastreio,
	}
}

func (*Servico) simularProcessamento(ctx context.Context, payload evento.DadosPagamentoAprovado) {
	log.Printf("Processando entrega para o pedido %s\n", payload.PedidoID)

	faixa := (TEMPO_PROCESSAMENTO_MIN_MS + rand.IntN(TEMPO_PROCESSAMENTO_MAX_MS-TEMPO_PROCESSAMENTO_MIN_MS))

	processamento_duracao := time.Duration(faixa) * time.Millisecond
	timer := time.NewTimer(processamento_duracao)

	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
		log.Println("Simulação de processamento cancelada...")
	}

	log.Printf("Entrega processada para o pedido %s\n", payload.PedidoID)
}

func (s *Servico) obterNotaFiscal() string {
	return fmt.Sprint(rand.IntN(899_999_999) + 100_000_000)
}

func (s *Servico) obterTransportadoraECodigoRastreio() (string, string) {
	transportadora := slices.Collect(maps.Keys(TRANSPORTADORAS_DISPONIVEIS))[rand.IntN(len(TRANSPORTADORAS_DISPONIVEIS))]
	codigo := rand.IntN(899_999_999) + 100_000_000
	codigo_rastreio := fmt.Sprintf("%s%d%s", TRANSPORTADORAS_DISPONIVEIS[transportadora], codigo, CODIGO_RASTREIO_SUFIXO)

	return transportadora, codigo_rastreio
}
