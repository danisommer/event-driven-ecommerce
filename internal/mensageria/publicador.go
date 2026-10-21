package mensageria

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"ecommerce/internal/cripto"
	"ecommerce/internal/evento"
)

// Publicador publica eventos na exchange, sempre assinados com a chave
// privada do microsserviço dono do chaveiro.
type Publicador struct {
	canal    *amqp.Channel
	chaveiro *cripto.Chaveiro
}

// NovoPublicador liga o publicador ao canal e ao chaveiro do serviço.
func NovoPublicador(conexao *Conexao, chaveiro *cripto.Chaveiro) *Publicador {
	return &Publicador{canal: conexao.Canal, chaveiro: chaveiro}
}

// Publicar monta o envelope, assina e envia como mensagem persistente.
func (p *Publicador) Publicar(ctx context.Context, exchange, routingKey string, dados any) error {
	env, err := evento.NovoEnvelope(routingKey, p.chaveiro.Servico, dados)
	if err != nil {
		return err
	}

	if err := cripto.AssinarEnvelope(p.chaveiro.Privada, &env); err != nil {
		return fmt.Errorf("publicando %s: %w", routingKey, err)
	}

	corpo, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("serializando envelope de %s: %w", routingKey, err)
	}

	err = p.canal.PublishWithContext(ctx,
		exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    env.ID,
			Type:         routingKey,
			AppId:        p.chaveiro.Servico,
			Timestamp:    time.Now(),
			Body:         corpo,
		},
	)
	if err != nil {
		return fmt.Errorf("publicando %s em %s: %w", routingKey, exchange, err)
	}

	registro.Printf("→ publicado %s (evento %s, assinado por %s)", routingKey, env.ID, p.chaveiro.Servico)
	return nil
}

// PublicarECommerce é o atalho para a exchange direct, por onde passam todos
// os eventos do sistema.
func (p *Publicador) PublicarECommerce(ctx context.Context, routingKey string, dados any) error {
	return p.Publicar(ctx, evento.ExchangeECommerce, routingKey, dados)
}
