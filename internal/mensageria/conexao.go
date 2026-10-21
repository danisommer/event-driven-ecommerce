// Package mensageria envolve o cliente AMQP e concentra tudo que os
// microsserviços fazem igual: conectar no broker, declarar as exchanges,
// publicar eventos já assinados e consumir eventos validando a assinatura.
package mensageria

import (
	"fmt"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"ecommerce/internal/evento"
)

// Conexao é a conexão TCP com o broker mais o canal AMQP por onde passam os
// comandos.
type Conexao struct {
	conn  *amqp.Connection
	Canal *amqp.Channel
}

// URLBroker devolve a URL de conexão, lendo RABBITMQ_URL quando definida.
func URLBroker() string {
	if url := os.Getenv("RABBITMQ_URL"); url != "" {
		return url
	}
	return "amqp://guest:guest@localhost:5672/"
}

// DiretorioChaves devolve a raiz das pastas de chaves, lendo CHAVES_DIR
// quando definida.
func DiretorioChaves() string {
	if dir := os.Getenv("CHAVES_DIR"); dir != "" {
		return dir
	}
	return "chaves"
}

// Conectar abre a conexão e o canal, tentando de novo por até um minuto.
func Conectar(url string) (*Conexao, error) {
	const tentativas = 20
	const intervalo = 3 * time.Second

	var ultimoErro error
	for i := 1; i <= tentativas; i++ {
		conn, err := amqp.Dial(url)
		if err == nil {
			canal, err := conn.Channel()
			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("abrindo canal AMQP: %w", err)
			}
			return &Conexao{conn: conn, Canal: canal}, nil
		}

		ultimoErro = err
		registro.Printf("broker indisponível (tentativa %d/%d): %v", i, tentativas, err)
		time.Sleep(intervalo)
	}

	return nil, fmt.Errorf("não consegui conectar em %s: %w", url, ultimoErro)
}

// DeclararExchanges cria as exchanges do sistema como durable, se ainda não
// existirem.
func (c *Conexao) DeclararExchanges() error {
	exchanges := []struct {
		nome string
		tipo string
	}{
		{evento.ExchangeECommerce, amqp.ExchangeDirect},
	}

	for _, e := range exchanges {
		err := c.Canal.ExchangeDeclare(
			e.nome,
			e.tipo,
			true,  // durable
			false, // auto-deleted
			false, // internal
			false, // no-wait
			nil,   // argumentos
		)
		if err != nil {
			return fmt.Errorf("declarando exchange %s (%s): %w", e.nome, e.tipo, err)
		}
	}
	return nil
}

// Fechar encerra canal e conexão.
func (c *Conexao) Fechar() {
	if c.Canal != nil {
		c.Canal.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
