package evento

import "time"

// ItemPedido é um produto e a quantidade solicitada dentro de um pedido.
type ItemPedido struct {
	ProdutoID  string  `json:"produto_id"`
	Nome       string  `json:"nome"`
	Quantidade int     `json:"quantidade"`
	PrecoUnit  float64 `json:"preco_unitario"`
}

// DadosPedidoCriado acompanha a routing key pedido.criado.
// Publicado pelo Principal, consumido pelo Estoque.
type DadosPedidoCriado struct {
	PedidoID string       `json:"pedido_id"`
	Cliente  string       `json:"cliente"`
	Itens    []ItemPedido `json:"itens"`
	Total    float64      `json:"total"`
}

// DadosPedidoExcluido acompanha a routing key pedido.excluido.
// Publicado pelo Principal, consumido pelo Estoque.
type DadosPedidoExcluido struct {
	PedidoID string `json:"pedido_id"`
	Motivo   string `json:"motivo"`
}

// DadosPedidoEstoqueOK acompanha a routing key pedido.estoque_ok.
// Publicado pelo Estoque, consumido pelo Pagamento e pelo Principal.
type DadosPedidoEstoqueOK struct {
	PedidoID string       `json:"pedido_id"`
	Itens    []ItemPedido `json:"itens"`
	Total    float64      `json:"total"`
}

// DadosEstoqueIndisponivel acompanha a routing key estoque.indisponivel.
// Publicado pelo Estoque, consumido pelo Principal.
type DadosEstoqueIndisponivel struct {
	PedidoID   string `json:"pedido_id"`
	ProdutoID  string `json:"produto_id"`
	Nome       string `json:"nome"`
	Solicitado int    `json:"solicitado"`
	Disponivel int    `json:"disponivel"`
	Motivo     string `json:"motivo"`
}

// DadosPagamentoPendente acompanha a routing key pagamento.pendente.
// Publicado pelo Pagamento, consumido pelo Principal. Leva a URL de checkout
// do provedor de pagamento, que o Principal repassa ao frontend.
type DadosPagamentoPendente struct {
	PedidoID    string  `json:"pedido_id"`
	CobrancaID  string  `json:"cobranca_id"`
	URLCheckout string  `json:"url_checkout"`
	Valor       float64 `json:"valor"`
}

// DadosPagamentoAprovado acompanha a routing key pagamento.aprovado.
// Publicado pelo Pagamento, consumido pela Entrega e pelo Principal.
// Leva os itens junto porque a Entrega precisa deles para emitir a nota.
type DadosPagamentoAprovado struct {
	PedidoID    string       `json:"pedido_id"`
	TransacaoID string       `json:"transacao_id"`
	Valor       float64      `json:"valor"`
	Itens       []ItemPedido `json:"itens"`
}

// DadosPagamentoRecusado acompanha a routing key pagamento.recusado.
// Publicado pelo Pagamento, consumido pelo Principal.
type DadosPagamentoRecusado struct {
	PedidoID string  `json:"pedido_id"`
	Valor    float64 `json:"valor"`
	Motivo   string  `json:"motivo"`
}

// DadosPedidoEnviado acompanha a routing key pedido.enviado.
// Publicado pela Entrega, consumido pelo Principal.
type DadosPedidoEnviado struct {
	PedidoID       string `json:"pedido_id"`
	NotaFiscal     string `json:"nota_fiscal"`
	Transportadora string `json:"transportadora"`
	CodigoRastreio string `json:"codigo_rastreio"`
}

// DadosInteressePromocao acompanha a routing key interesse.promocao.
// Publicado pelo Principal, consumido pelo Promoções. Substitui as categorias
// de interesse já cadastradas para o e-mail.
type DadosInteressePromocao struct {
	Email      string   `json:"email"`
	Categorias []string `json:"categorias"`
}

// DadosInteresseCancelado acompanha a routing key interesse.cancelado.
// Publicado pelo Principal, consumido pelo Promoções.
type DadosInteresseCancelado struct {
	Email string `json:"email"`
}

// DadosPromocaoCriada acompanha a routing key promocao.criada.
// Publicado pelo Promoções, consumido pelo Estoque, que passa a vender o
// produto pelo preço promocional até a validade.
type DadosPromocaoCriada struct {
	ProdutoID        string    `json:"produto_id"`
	PrecoPromocional float64   `json:"preco_promocional"`
	Desconto         int       `json:"desconto_percentual"`
	ValidaAte        time.Time `json:"valida_ate"`
}
