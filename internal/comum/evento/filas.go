package evento

// Nomes das filas do sistema. Cada consumidor tem a sua própria fila.
const (
	FilaPrincipal = "principal.status"
	FilaEstoque   = "estoque.pedidos"
	FilaPagamento = "pagamento.estoque_ok"
	FilaEntrega   = "entrega.pagamentos"
	FilaPromocoes = "promocoes.interesses"
)
