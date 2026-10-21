package estoque

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"

	"ecommerce/internal/catalogo"
)

// Produto é um produto do estoque com o saldo atual. Preco é o preço que vale
// agora: com uma promoção válida, é o promocional, e PrecoOriginal guarda o
// de tabela.
type Produto struct {
	ID            string  `json:"id"`
	Nome          string  `json:"nome"`
	Categoria     string  `json:"categoria"`
	Preco         float64 `json:"preco"`
	PrecoOriginal float64 `json:"preco_original,omitempty"`
	Quantidade    int     `json:"quantidade"`
}

var esquema = []string{
	`CREATE TABLE IF NOT EXISTS produtos (
		id         TEXT PRIMARY KEY,
		nome       TEXT NOT NULL,
		categoria  TEXT NOT NULL,
		preco      REAL NOT NULL,
		quantidade INTEGER NOT NULL CHECK (quantidade >= 0)
	)`,
	`CREATE TABLE IF NOT EXISTS reservas (
		pedido_id  TEXT NOT NULL,
		produto_id TEXT NOT NULL,
		quantidade INTEGER NOT NULL CHECK (quantidade > 0),
		PRIMARY KEY (pedido_id, produto_id)
	)`,
	`CREATE TABLE IF NOT EXISTS promocoes (
		produto_id TEXT PRIMARY KEY,
		preco      REAL NOT NULL CHECK (preco > 0),
		valida_ate INTEGER NOT NULL
	)`,
}

// consultaProdutos junta cada produto à sua promoção ainda válida, se houver.
// O parâmetro é o instante atual em segundos Unix.
const consultaProdutos = `SELECT p.id, p.nome, p.categoria, p.preco, p.quantidade, pr.preco
	FROM produtos p
	LEFT JOIN promocoes pr ON pr.produto_id = p.id AND pr.valida_ate > ?`

// Repositorio guarda os produtos, as reservas por pedido e as promoções num
// banco SQLite.
//
// Usa uma única conexão: o SQLite aceita um escritor por vez, e assim toda
// operação entra em fila, como fazia o mutex da versão em memória. Por isso
// nenhum método do Repositorio pode ser chamado com uma Transacao aberta.
type Repositorio struct {
	db *sql.DB
}

// AbrirRepositorio abre (ou cria) o banco em caminho, cria as tabelas que
// faltarem e, se ainda não houver produtos, grava o catálogo com as
// quantidades iniciais.
func AbrirRepositorio(caminho string) (*Repositorio, error) {
	db, err := sql.Open("sqlite", caminho)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco %s: %w", caminho, err)
	}
	db.SetMaxOpenConns(1)

	r := &Repositorio{db: db}
	if err := r.preparar(); err != nil {
		db.Close()
		return nil, fmt.Errorf("preparando banco %s: %w", caminho, err)
	}
	return r, nil
}

// Fechar encerra a conexão com o banco.
func (r *Repositorio) Fechar() error {
	return r.db.Close()
}

func (r *Repositorio) preparar() error {
	for _, comando := range esquema {
		if _, err := r.db.Exec(comando); err != nil {
			return fmt.Errorf("criando tabelas: %w", err)
		}
	}
	return r.semear()
}

// semear grava o catálogo com as quantidades iniciais quando o banco ainda
// não tem nenhum produto.
func (r *Repositorio) semear() error {
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM produtos`).Scan(&total); err != nil {
		return fmt.Errorf("contando produtos: %w", err)
	}
	if total > 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("iniciando carga inicial: %w", err)
	}
	defer tx.Rollback()

	for _, p := range catalogo.Produtos {
		_, err := tx.Exec(`INSERT INTO produtos (id, nome, categoria, preco, quantidade) VALUES (?, ?, ?, ?, ?)`,
			p.ID, p.Nome, p.Categoria, p.Preco, p.QuantidadeInicial)
		if err != nil {
			return fmt.Errorf("gravando %s na carga inicial: %w", p.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmando carga inicial: %w", err)
	}

	log.Printf("banco sem produtos: catálogo gravado com as quantidades iniciais")
	return nil
}

// Disponiveis lista os produtos com saldo positivo, em ordem de ID.
func (r *Repositorio) Disponiveis(ctx context.Context) ([]Produto, error) {
	return r.listar(ctx, consultaProdutos+` WHERE p.quantidade > 0 ORDER BY p.id`)
}

// Todos lista todos os produtos, com qualquer saldo, em ordem de ID.
func (r *Repositorio) Todos(ctx context.Context) ([]Produto, error) {
	return r.listar(ctx, consultaProdutos+` ORDER BY p.id`)
}

// Buscar devolve o produto de um ID, com qualquer saldo. O segundo retorno é
// falso quando o produto não existe.
func (r *Repositorio) Buscar(ctx context.Context, id string) (Produto, bool, error) {
	return buscar(ctx, r.db, id)
}

// RegistrarPromocao grava, ou troca, o preço promocional do produto, válido
// até validaAte.
func (r *Repositorio) RegistrarPromocao(ctx context.Context, produtoID string, preco float64, validaAte time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO promocoes (produto_id, preco, valida_ate) VALUES (?, ?, ?)
		ON CONFLICT (produto_id) DO UPDATE SET preco = excluded.preco, valida_ate = excluded.valida_ate`,
		produtoID, preco, validaAte.Unix())
	if err != nil {
		return fmt.Errorf("gravando a promoção de %s: %w", produtoID, err)
	}
	return nil
}

func (r *Repositorio) listar(ctx context.Context, consulta string) ([]Produto, error) {
	linhas, err := r.db.QueryContext(ctx, consulta, time.Now().Unix())
	if err != nil {
		return nil, fmt.Errorf("consultando produtos: %w", err)
	}
	defer linhas.Close()

	produtos := []Produto{}
	for linhas.Next() {
		p, err := lerProduto(linhas)
		if err != nil {
			return nil, fmt.Errorf("lendo produto: %w", err)
		}
		produtos = append(produtos, p)
	}
	if err := linhas.Err(); err != nil {
		return nil, fmt.Errorf("percorrendo produtos: %w", err)
	}
	return produtos, nil
}

type consultaDeLinha interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func buscar(ctx context.Context, c consultaDeLinha, id string) (Produto, bool, error) {
	p, err := lerProduto(c.QueryRowContext(ctx, consultaProdutos+` WHERE p.id = ?`, time.Now().Unix(), id))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Produto{}, false, nil
	case err != nil:
		return Produto{}, false, fmt.Errorf("buscando produto %s: %w", id, err)
	default:
		return p, true, nil
	}
}

type linhaDeProduto interface {
	Scan(destinos ...any) error
}

// lerProduto lê uma linha de consultaProdutos e aplica o preço promocional,
// quando a linha trouxer um.
func lerProduto(linha linhaDeProduto) (Produto, error) {
	var p Produto
	var promocional sql.NullFloat64
	if err := linha.Scan(&p.ID, &p.Nome, &p.Categoria, &p.Preco, &p.Quantidade, &promocional); err != nil {
		return Produto{}, err
	}

	if promocional.Valid {
		p.PrecoOriginal = p.Preco
		p.Preco = promocional.Float64
	}
	return p, nil
}

// Transacao agrupa as operações de uma reserva ou de uma devolução: ou todas
// valem, ou nenhuma vale.
type Transacao struct {
	ctx context.Context
	tx  *sql.Tx
}

// Iniciar abre uma transação. Quem chama deve adiar Desfazer com defer e
// chamar Confirmar quando tudo der certo.
func (r *Repositorio) Iniciar(ctx context.Context) (*Transacao, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("iniciando transação: %w", err)
	}
	return &Transacao{ctx: ctx, tx: tx}, nil
}

// Confirmar grava de vez tudo o que a transação fez.
func (t *Transacao) Confirmar() error {
	if err := t.tx.Commit(); err != nil {
		return fmt.Errorf("confirmando transação: %w", err)
	}
	return nil
}

// Desfazer descarta o que a transação fez. Depois de Confirmar, não faz nada.
func (t *Transacao) Desfazer() {
	if err := t.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Printf("desfazendo transação: %v", err)
	}
}

// Reservado diz se o pedido já tem reserva gravada.
func (t *Transacao) Reservado(pedidoID string) (bool, error) {
	var existe bool
	err := t.tx.QueryRowContext(t.ctx,
		`SELECT EXISTS (SELECT 1 FROM reservas WHERE pedido_id = ?)`, pedidoID).Scan(&existe)
	if err != nil {
		return false, fmt.Errorf("conferindo reserva do pedido %s: %w", pedidoID, err)
	}
	return existe, nil
}

// Produto devolve o produto de um ID, lido dentro da transação.
func (t *Transacao) Produto(id string) (Produto, bool, error) {
	return buscar(t.ctx, t.tx, id)
}

// Reservar baixa a quantidade do saldo do produto e grava a reserva do pedido.
func (t *Transacao) Reservar(pedidoID, produtoID string, quantidade int) error {
	_, err := t.tx.ExecContext(t.ctx,
		`UPDATE produtos SET quantidade = quantidade - ? WHERE id = ?`, quantidade, produtoID)
	if err != nil {
		return fmt.Errorf("baixando %d de %s: %w", quantidade, produtoID, err)
	}

	_, err = t.tx.ExecContext(t.ctx,
		`INSERT INTO reservas (pedido_id, produto_id, quantidade) VALUES (?, ?, ?)`,
		pedidoID, produtoID, quantidade)
	if err != nil {
		return fmt.Errorf("gravando reserva de %s no pedido %s: %w", produtoID, pedidoID, err)
	}
	return nil
}

// Devolver repõe no saldo tudo o que estava reservado para o pedido e apaga a
// reserva. Devolve quantos produtos foram repostos, zero quando o pedido não
// tinha reserva.
func (t *Transacao) Devolver(pedidoID string) (int, error) {
	reservados, err := t.reservados(pedidoID)
	if err != nil {
		return 0, err
	}

	for produtoID, quantidade := range reservados {
		_, err := t.tx.ExecContext(t.ctx,
			`UPDATE produtos SET quantidade = quantidade + ? WHERE id = ?`, quantidade, produtoID)
		if err != nil {
			return 0, fmt.Errorf("repondo %d de %s: %w", quantidade, produtoID, err)
		}
	}

	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM reservas WHERE pedido_id = ?`, pedidoID); err != nil {
		return 0, fmt.Errorf("apagando reserva do pedido %s: %w", pedidoID, err)
	}
	return len(reservados), nil
}

func (t *Transacao) reservados(pedidoID string) (map[string]int, error) {
	linhas, err := t.tx.QueryContext(t.ctx,
		`SELECT produto_id, quantidade FROM reservas WHERE pedido_id = ?`, pedidoID)
	if err != nil {
		return nil, fmt.Errorf("lendo reserva do pedido %s: %w", pedidoID, err)
	}
	defer linhas.Close()

	reservados := make(map[string]int)
	for linhas.Next() {
		var produtoID string
		var quantidade int
		if err := linhas.Scan(&produtoID, &quantidade); err != nil {
			return nil, fmt.Errorf("lendo reserva do pedido %s: %w", pedidoID, err)
		}
		reservados[produtoID] = quantidade
	}
	if err := linhas.Err(); err != nil {
		return nil, fmt.Errorf("percorrendo reserva do pedido %s: %w", pedidoID, err)
	}
	return reservados, nil
}
