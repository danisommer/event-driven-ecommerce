// Package rest reúne o que os microsserviços com API HTTP fazem igual: ler a
// porta do ambiente, responder JSON e atender HTTP junto com o consumo da
// fila, encerrando os dois quando o processo recebe Ctrl+C.
package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// tempoEncerramento é quanto o servidor espera as requisições em andamento
// terminarem antes de desligar.
const tempoEncerramento = 5 * time.Second

// Erro é o corpo de toda resposta de erro das APIs.
type Erro struct {
	Erro string `json:"erro"`
}

// Ambiente lê a variável de ambiente nome, ou devolve padrao se ela não
// estiver definida.
func Ambiente(nome, padrao string) string {
	if valor := os.Getenv(nome); valor != "" {
		return valor
	}
	return padrao
}

// Endereco devolve o endereço de escuta, lendo a porta da variável de
// ambiente informada ou usando portaPadrao.
func Endereco(variavel, portaPadrao string) string {
	return ":" + Ambiente(variavel, portaPadrao)
}

// ResponderJSON escreve corpo como JSON com o status informado.
func ResponderJSON(w http.ResponseWriter, status int, corpo any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(corpo); err != nil {
		log.Printf("escrevendo resposta JSON: %v", err)
	}
}

// ResponderErro escreve {"erro": mensagem} com o status informado.
func ResponderErro(w http.ResponseWriter, status int, mensagem string) {
	ResponderJSON(w, status, Erro{Erro: mensagem})
}

// Rodar atende HTTP com servidor e executa consumir ao mesmo tempo. Devolve o
// primeiro erro de qualquer um dos dois. Com o contexto cancelado, espera os
// dois terminarem e devolve nil. As requisições herdam ctx, então conexões
// longas, como as de SSE, também são encerradas quando ele é cancelado.
func Rodar(ctx context.Context, servidor *http.Server, consumir func(context.Context) error) error {
	erros := make(chan error, 2)
	go func() { erros <- servir(ctx, servidor) }()
	go func() { erros <- consumir(ctx) }()

	if err := <-erros; err != nil {
		return err
	}
	return <-erros
}

func servir(ctx context.Context, servidor *http.Server) error {
	servidor.BaseContext = func(net.Listener) context.Context { return ctx }

	falha := make(chan error, 1)
	go func() {
		log.Printf("API HTTP ouvindo em %s", servidor.Addr)
		falha <- servidor.ListenAndServe()
	}()

	select {
	case err := <-falha:
		return fmt.Errorf("servidor HTTP em %s: %w", servidor.Addr, err)
	case <-ctx.Done():
		encerrar, cancelar := context.WithTimeout(context.Background(), tempoEncerramento)
		defer cancelar()
		if err := servidor.Shutdown(encerrar); err != nil {
			return fmt.Errorf("encerrando servidor HTTP: %w", err)
		}
		return nil
	}
}
