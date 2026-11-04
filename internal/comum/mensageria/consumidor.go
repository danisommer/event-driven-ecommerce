package mensageria

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"

	"ecommerce/internal/comum/cripto"
	"ecommerce/internal/comum/evento"
)

// Tratador processa um evento já validado. Devolver erro faz a mensagem ser
// descartada com log, sem voltar para a fila.
type Tratador func(ctx context.Context, env evento.Envelope) error

// Consumidor é a fila própria de um microsserviço mais o laço que lê dela.
type Consumidor struct {
	canal     *amqp.Channel
	fila      string
	chaveiro  *cripto.Chaveiro
	verificar bool
}

// VerificacaoAtiva diz se a assinatura dos eventos recebidos deve ser
// validada, lendo VERIFICAR_ASSINATURA. O padrão é sim.
func VerificacaoAtiva() bool {
	valor := strings.ToLower(strings.TrimSpace(os.Getenv("VERIFICAR_ASSINATURA")))
	return valor != "off" && valor != "0" && valor != "false" && valor != "nao"
}

// NovoConsumidor declara a fila durable do microsserviço, com prefetch de uma
// mensagem por vez, e prepara o laço de leitura.
func NovoConsumidor(conexao *Conexao, fila string, chaveiro *cripto.Chaveiro) (*Consumidor, error) {
	_, err := conexao.Canal.QueueDeclare(
		fila,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // argumentos
	)
	if err != nil {
		return nil, fmt.Errorf("declarando fila %s: %w", fila, err)
	}

	if err := conexao.Canal.Qos(1, 0, false); err != nil {
		return nil, fmt.Errorf("configurando prefetch da fila %s: %w", fila, err)
	}

	c := &Consumidor{
		canal:     conexao.Canal,
		fila:      fila,
		chaveiro:  chaveiro,
		verificar: VerificacaoAtiva(),
	}

	if !c.verificar {
		registro.Printf("ATENÇÃO: VERIFICAR_ASSINATURA=off — a fila %s vai aceitar eventos sem validar a assinatura", fila)
	}

	return c, nil
}

// Vincular liga a fila a uma ou mais binding keys de uma exchange.
func (c *Consumidor) Vincular(exchange string, chaves ...string) error {
	for _, chave := range chaves {
		err := c.canal.QueueBind(
			c.fila,
			chave,
			exchange,
			false, // no-wait
			nil,   // argumentos
		)
		if err != nil {
			return fmt.Errorf("vinculando fila %s a %s com a chave %s: %w", c.fila, exchange, chave, err)
		}
		registro.Printf("fila %s vinculada a %s com a binding key %q", c.fila, exchange, chave)
	}
	return nil
}

// Consumir bloqueia lendo a fila até o contexto ser cancelado ou a conexão
// cair.
func (c *Consumidor) Consumir(ctx context.Context, tratar Tratador) error {
	entregas, err := c.canal.Consume(
		c.fila,
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // argumentos
	)
	if err != nil {
		return fmt.Errorf("consumindo a fila %s: %w", c.fila, err)
	}

	registro.Printf("aguardando eventos na fila %s", c.fila)

	for {
		select {
		case <-ctx.Done():
			return nil

		case entrega, aberto := <-entregas:
			if !aberto {
				return fmt.Errorf("a fila %s parou de entregar (conexão com o broker caiu)", c.fila)
			}
			c.processar(ctx, entrega, tratar)
		}
	}
}

func (c *Consumidor) processar(ctx context.Context, entrega amqp.Delivery, tratar Tratador) {
	var env evento.Envelope
	if err := json.Unmarshal(entrega.Body, &env); err != nil {
		registro.Printf("mensagem ilegível na fila %s, DESCARTADA: %v", c.fila, err)
		entrega.Nack(false, false)
		return
	}

	if c.verificar {
		if err := cripto.VerificarEnvelope(c.chaveiro, env); err != nil {
			registro.Printf("assinatura inválida no evento %s vindo de %q, DESCARTADO: %v", env.Tipo, env.Produtor, err)
			entrega.Nack(false, false)
			return
		}
	}

	registro.Printf("← recebido %s (evento %s, produtor %s)", env.Tipo, env.ID, env.Produtor)

	if err := tratar(ctx, env); err != nil {
		registro.Printf("erro tratando %s (evento %s): %v", env.Tipo, env.ID, err)
		entrega.Nack(false, false)
		return
	}

	entrega.Ack(false)
}
