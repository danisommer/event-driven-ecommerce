// Package catalogo guarda a lista fixa de produtos vendidos pela loja.
package catalogo

// Produto é uma linha do catálogo. QuantidadeInicial só vale na carga inicial
// do banco do Estoque, quando ele ainda não tem produtos.
type Produto struct {
	ID                string
	Nome              string
	Categoria         string
	Preco             float64
	QuantidadeInicial int
}

// Produtos é a vitrine completa, dividida nas categorias A, B e C usadas
// pelas promoções.
var Produtos = []Produto{
	{ID: "P001", Nome: "Notebook Gamer 16GB", Categoria: "A", Preco: 4500.00, QuantidadeInicial: 10},
	{ID: "P002", Nome: "Mouse sem fio", Categoria: "A", Preco: 150.00, QuantidadeInicial: 50},
	{ID: "P003", Nome: "Teclado mecânico", Categoria: "A", Preco: 320.00, QuantidadeInicial: 25},
	{ID: "P004", Nome: "Cafeteira expresso", Categoria: "B", Preco: 280.00, QuantidadeInicial: 15},
	{ID: "P005", Nome: "Liquidificador 900W", Categoria: "B", Preco: 190.00, QuantidadeInicial: 20},
	{ID: "P006", Nome: "Air Fryer 5L", Categoria: "B", Preco: 450.00, QuantidadeInicial: 8},
	{ID: "P007", Nome: "Camiseta algodão", Categoria: "C", Preco: 79.90, QuantidadeInicial: 100},
	{ID: "P008", Nome: "Tênis de corrida", Categoria: "C", Preco: 350.00, QuantidadeInicial: 30},
	{ID: "P009", Nome: "Mochila 30L", Categoria: "C", Preco: 210.00, QuantidadeInicial: 12},
}

// Categorias lista as categorias existentes, na ordem em que aparecem.
var Categorias = []string{"A", "B", "C"}

// Buscar devolve o produto de um dado ID.
func Buscar(id string) (Produto, bool) {
	for _, p := range Produtos {
		if p.ID == id {
			return p, true
		}
	}
	return Produto{}, false
}

// PorCategoria devolve todos os produtos de uma categoria.
func PorCategoria(categoria string) []Produto {
	var achados []Produto
	for _, p := range Produtos {
		if p.Categoria == categoria {
			achados = append(achados, p)
		}
	}
	return achados
}
