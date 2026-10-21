// Package evento define o envelope comum a todos os eventos do sistema, os
// nomes das exchanges e as routing keys usadas pelos microsserviços.
package evento

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ExchangeECommerce é a exchange do tipo direct por onde passam todos os
// eventos do sistema.
const ExchangeECommerce = "eCommerce"

// Routing keys da exchange eCommerce.
const (
	PedidoCriado        = "pedido.criado"
	PedidoExcluido      = "pedido.excluido"
	PedidoEstoqueOK     = "pedido.estoque_ok"
	EstoqueIndisponivel = "estoque.indisponivel"
	PagamentoPendente   = "pagamento.pendente"
	PagamentoAprovado   = "pagamento.aprovado"
	PagamentoRecusado   = "pagamento.recusado"
	PedidoEnviado       = "pedido.enviado"
	InteressePromocao   = "interesse.promocao"
	InteresseCancelado  = "interesse.cancelado"
	PromocaoCriada      = "promocao.criada"
)

// Nomes dos microsserviços. O nome identifica o produtor do evento e também a
// pasta de chaves usada por ele, em chaves/<nome>.
const (
	ServicoPrincipal = "principal"
	ServicoEstoque   = "estoque"
	ServicoPagamento = "pagamento"
	ServicoEntrega   = "entrega"
	ServicoPromocoes = "promocoes"
)

// Servicos lista todos os microsserviços que assinam eventos. É usada para
// gerar os pares de chaves e para distribuir as chaves públicas.
var Servicos = []string{
	ServicoPrincipal,
	ServicoEstoque,
	ServicoPagamento,
	ServicoEntrega,
	ServicoPromocoes,
}

// Envelope é o formato único de toda mensagem que trafega pelo broker. Dados
// carrega o payload específico do evento e Signature carrega a assinatura
// digital em base64, produzida com a chave privada de Produtor.
type Envelope struct {
	ID        string          `json:"id"`
	Tipo      string          `json:"tipo"`
	Produtor  string          `json:"produtor"`
	Timestamp string          `json:"timestamp"`
	Dados     json.RawMessage `json:"dados"`
	Signature string          `json:"signature"`
}

// NovoEnvelope monta um envelope ainda sem assinatura, serializando dados
// como JSON no campo Dados. Quem assina é o publicador.
func NovoEnvelope(tipo, produtor string, dados any) (Envelope, error) {
	bruto, err := json.Marshal(dados)
	if err != nil {
		return Envelope{}, fmt.Errorf("serializando dados do evento %s: %w", tipo, err)
	}
	return Envelope{
		ID:        novoID(),
		Tipo:      tipo,
		Produtor:  produtor,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Dados:     bruto,
	}, nil
}

// BytesCanonicos devolve a representação determinística do evento, sem o
// campo Signature, que é o conteúdo hasheado e assinado. Produtor e
// consumidor precisam chegar aos mesmos bytes, por isso Dados é mantido como
// json.RawMessage.
func (e Envelope) BytesCanonicos() ([]byte, error) {
	semAssinatura := struct {
		ID        string          `json:"id"`
		Tipo      string          `json:"tipo"`
		Produtor  string          `json:"produtor"`
		Timestamp string          `json:"timestamp"`
		Dados     json.RawMessage `json:"dados"`
	}{
		ID:        e.ID,
		Tipo:      e.Tipo,
		Produtor:  e.Produtor,
		Timestamp: e.Timestamp,
		Dados:     e.Dados,
	}

	conteudo, err := json.Marshal(semAssinatura)
	if err != nil {
		return nil, fmt.Errorf("montando bytes canônicos do evento %s: %w", e.Tipo, err)
	}
	return conteudo, nil
}

// DecodificarDados desserializa o payload do envelope em destino.
func (e Envelope) DecodificarDados(destino any) error {
	if err := json.Unmarshal(e.Dados, destino); err != nil {
		return fmt.Errorf("decodificando dados do evento %s: %w", e.Tipo, err)
	}
	return nil
}

func novoID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}
