// Package pagamento implementa o microsserviço de pagamentos.
//
// Consome pedido.estoque_ok e pedido.excluido. Para cada pedido com estoque
// confirmado, cria uma cobrança no provedor externo (o Mock de Pagamento) e
// publica pagamento.pendente com a URL de checkout. Quando o provedor avisa o
// resultado pelo webhook, publica pagamento.aprovado ou pagamento.recusado.
package pagamento

import (
	"context"
	"errors"
	"log"
	"sync"

	"ecommerce/internal/evento"
)

// Publicador é o pedaço da mensageria que o Pagamento usa.
type Publicador interface {
	PublicarECommerce(ctx context.Context, routingKey string, dados any) error
}

// Provedor é o provedor de pagamentos externo.
type Provedor interface {
	CriarCobranca(ctx context.Context, pedidoID string, valor float64) (CobrancaCriada, error)
}

// Decisões que o provedor envia pelo webhook.
const (
	DecisaoAprovado = "APROVADO"
	DecisaoRecusado = "RECUSADO"
)

// Erros de Decidir, que a API traduz em status HTTP.
var (
	ErrDecisaoInvalida      = errors.New("status deve ser APROVADO ou RECUSADO")
	ErrCobrancaDesconhecida = errors.New("cobrança desconhecida")
	ErrPedidoDivergente     = errors.New("a cobrança não pertence a este pedido")
)

const (
	motivoSemCobranca = "não foi possível criar a cobrança no provedor de pagamento"
	motivoRecusado    = "recusado no provedor de pagamento"
)

type situacao string

const (
	pendente  situacao = "pendente"
	aprovada  situacao = "aprovada"
	recusada  situacao = "recusada"
	cancelada situacao = "cancelada"
)

// cobranca é o que o Pagamento sabe de um pedido. id fica vazio quando o
// provedor não chegou a criar a cobrança.
type cobranca struct {
	id       string
	pedido   evento.DadosPedidoEstoqueOK
	situacao situacao
}

// Servico guarda, em memória, a cobrança de cada pedido e reage aos eventos e
// ao webhook do provedor. O mutex protege os mapas, que são lidos pelo
// consumidor e pelas requisições HTTP ao mesmo tempo.
type Servico struct {
	mu        sync.Mutex
	porPedido map[string]*cobranca
	porID     map[string]*cobranca

	provedor   Provedor
	publicador Publicador
}

// NovoServico liga o serviço ao provedor externo e ao broker.
func NovoServico(provedor Provedor, publicador Publicador) *Servico {
	return &Servico{
		porPedido:  make(map[string]*cobranca),
		porID:      make(map[string]*cobranca),
		provedor:   provedor,
		publicador: publicador,
	}
}

// Tratar é o ponto de entrada do consumidor: despacha pela routing key.
func (s *Servico) Tratar(ctx context.Context, env evento.Envelope) error {
	switch env.Tipo {
	case evento.PedidoEstoqueOK:
		return s.aoEstoqueOK(ctx, env)
	case evento.PedidoExcluido:
		return s.aoPedidoExcluido(env)
	default:
		log.Printf("evento %s ignorado: não é assunto do Pagamento", env.Tipo)
		return nil
	}
}

// aoEstoqueOK cria a cobrança no provedor e publica pagamento.pendente com a
// URL de checkout. Sem conseguir criar a cobrança, publica pagamento.recusado.
func (s *Servico) aoEstoqueOK(ctx context.Context, env evento.Envelope) error {
	var pedido evento.DadosPedidoEstoqueOK
	if err := env.DecodificarDados(&pedido); err != nil {
		return err
	}

	if anterior, existe := s.situacaoDo(pedido.PedidoID); existe {
		log.Printf("pedido %s já passou pelo Pagamento (%s), reentrega ignorada", pedido.PedidoID, anterior)
		return nil
	}

	criada, err := s.provedor.CriarCobranca(ctx, pedido.PedidoID, pedido.Total)
	if err != nil {
		log.Printf("✗ pedido %s sem cobrança: %v", pedido.PedidoID, err)
		s.guardar(&cobranca{pedido: pedido, situacao: recusada})
		return s.publicador.PublicarECommerce(ctx, evento.PagamentoRecusado, evento.DadosPagamentoRecusado{
			PedidoID: pedido.PedidoID,
			Valor:    pedido.Total,
			Motivo:   motivoSemCobranca,
		})
	}

	s.guardar(&cobranca{id: criada.ID, pedido: pedido, situacao: pendente})
	log.Printf("pedido %s: cobrança %s criada, aguardando o comprador em %s", pedido.PedidoID, criada.ID, criada.URLCheckout)

	return s.publicador.PublicarECommerce(ctx, evento.PagamentoPendente, evento.DadosPagamentoPendente{
		PedidoID:    pedido.PedidoID,
		CobrancaID:  criada.ID,
		URLCheckout: criada.URLCheckout,
		Valor:       pedido.Total,
	})
}

// aoPedidoExcluido cancela a cobrança pendente do pedido, para que uma decisão
// que chegue depois pelo webhook seja ignorada. Se o pedido ainda não passou
// pelo Pagamento, registra o cancelamento para a cobrança nem ser criada.
func (s *Servico) aoPedidoExcluido(env evento.Envelope) error {
	var excluido evento.DadosPedidoExcluido
	if err := env.DecodificarDados(&excluido); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, existe := s.porPedido[excluido.PedidoID]
	switch {
	case !existe:
		s.porPedido[excluido.PedidoID] = &cobranca{
			pedido:   evento.DadosPedidoEstoqueOK{PedidoID: excluido.PedidoID},
			situacao: cancelada,
		}
		log.Printf("pedido %s excluído (%s) antes de chegar ao Pagamento: nenhuma cobrança será criada",
			excluido.PedidoID, excluido.Motivo)
	case c.situacao == pendente:
		c.situacao = cancelada
		log.Printf("pedido %s excluído (%s): cobrança %s cancelada", excluido.PedidoID, excluido.Motivo, c.id)
	default:
		log.Printf("pedido %s excluído (%s): o pagamento já estava %s, nada a fazer",
			excluido.PedidoID, excluido.Motivo, c.situacao)
	}
	return nil
}

// Decidir aplica a decisão do provedor sobre uma cobrança e publica
// pagamento.aprovado ou pagamento.recusado. Decisão repetida ou sobre
// cobrança cancelada é ignorada sem erro, para o provedor não reenviar. Se a
// publicação falhar, a cobrança continua pendente e aceita nova tentativa.
func (s *Servico) Decidir(ctx context.Context, cobrancaID, pedidoID, decisao string) error {
	if decisao != DecisaoAprovado && decisao != DecisaoRecusado {
		return ErrDecisaoInvalida
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, existe := s.porID[cobrancaID]
	switch {
	case !existe:
		return ErrCobrancaDesconhecida
	case c.pedido.PedidoID != pedidoID:
		return ErrPedidoDivergente
	case c.situacao != pendente:
		log.Printf("cobrança %s já está %s: aviso %s ignorado", cobrancaID, c.situacao, decisao)
		return nil
	}

	nova, err := s.publicarDecisao(ctx, c, decisao)
	if err != nil {
		return err
	}
	c.situacao = nova
	return nil
}

func (s *Servico) publicarDecisao(ctx context.Context, c *cobranca, decisao string) (situacao, error) {
	switch decisao {
	case DecisaoAprovado:
		log.Printf("✓ pagamento do pedido %s APROVADO (cobrança %s)", c.pedido.PedidoID, c.id)
		return aprovada, s.publicador.PublicarECommerce(ctx, evento.PagamentoAprovado, evento.DadosPagamentoAprovado{
			PedidoID:    c.pedido.PedidoID,
			TransacaoID: c.id,
			Valor:       c.pedido.Total,
			Itens:       c.pedido.Itens,
		})
	default:
		log.Printf("✗ pagamento do pedido %s RECUSADO (cobrança %s)", c.pedido.PedidoID, c.id)
		return recusada, s.publicador.PublicarECommerce(ctx, evento.PagamentoRecusado, evento.DadosPagamentoRecusado{
			PedidoID: c.pedido.PedidoID,
			Valor:    c.pedido.Total,
			Motivo:   motivoRecusado,
		})
	}
}

func (s *Servico) situacaoDo(pedidoID string) (situacao, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, existe := s.porPedido[pedidoID]
	if !existe {
		return "", false
	}
	return c.situacao, true
}

func (s *Servico) guardar(c *cobranca) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.porPedido[c.pedido.PedidoID] = c
	if c.id != "" {
		s.porID[c.id] = c
	}
}
