package promocoes

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const esquema = `CREATE TABLE IF NOT EXISTS inscricoes (
	email     TEXT NOT NULL,
	categoria TEXT NOT NULL,
	PRIMARY KEY (email, categoria)
)`

// Repositorio guarda as inscrições, um e-mail e uma categoria de interesse
// por linha, num banco SQLite com uma única conexão.
type Repositorio struct {
	db *sql.DB
}

// AbrirRepositorio abre (ou cria) o banco em caminho e a tabela de inscrições.
func AbrirRepositorio(caminho string) (*Repositorio, error) {
	db, err := sql.Open("sqlite", caminho)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco %s: %w", caminho, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(esquema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criando a tabela de inscrições em %s: %w", caminho, err)
	}
	return &Repositorio{db: db}, nil
}

// Fechar encerra a conexão com o banco.
func (r *Repositorio) Fechar() error {
	return r.db.Close()
}

// Inscrever troca as categorias do e-mail pelas informadas, numa transação.
func (r *Repositorio) Inscrever(ctx context.Context, email string, categorias []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciando a inscrição de %s: %w", email, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM inscricoes WHERE email = ?`, email); err != nil {
		return fmt.Errorf("limpando as inscrições de %s: %w", email, err)
	}
	for _, categoria := range categorias {
		_, err := tx.ExecContext(ctx, `INSERT INTO inscricoes (email, categoria) VALUES (?, ?)`, email, categoria)
		if err != nil {
			return fmt.Errorf("inscrevendo %s na categoria %s: %w", email, categoria, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmando a inscrição de %s: %w", email, err)
	}
	return nil
}

// Cancelar apaga todas as inscrições do e-mail e diz quantas havia.
func (r *Repositorio) Cancelar(ctx context.Context, email string) (int64, error) {
	resultado, err := r.db.ExecContext(ctx, `DELETE FROM inscricoes WHERE email = ?`, email)
	if err != nil {
		return 0, fmt.Errorf("cancelando as inscrições de %s: %w", email, err)
	}
	return resultado.RowsAffected()
}

// Inscritos devolve os e-mails inscritos na categoria, em ordem alfabética.
func (r *Repositorio) Inscritos(ctx context.Context, categoria string) ([]string, error) {
	return r.textos(ctx, `SELECT email FROM inscricoes WHERE categoria = ? ORDER BY email`, categoria)
}

// CategoriasComInscritos devolve as categorias com ao menos um inscrito, em
// ordem alfabética.
func (r *Repositorio) CategoriasComInscritos(ctx context.Context) ([]string, error) {
	return r.textos(ctx, `SELECT DISTINCT categoria FROM inscricoes ORDER BY categoria`)
}

func (r *Repositorio) textos(ctx context.Context, consulta string, args ...any) ([]string, error) {
	linhas, err := r.db.QueryContext(ctx, consulta, args...)
	if err != nil {
		return nil, fmt.Errorf("consultando inscrições: %w", err)
	}
	defer linhas.Close()

	textos := []string{}
	for linhas.Next() {
		var texto string
		if err := linhas.Scan(&texto); err != nil {
			return nil, fmt.Errorf("lendo inscrição: %w", err)
		}
		textos = append(textos, texto)
	}
	if err := linhas.Err(); err != nil {
		return nil, fmt.Errorf("percorrendo inscrições: %w", err)
	}
	return textos, nil
}
