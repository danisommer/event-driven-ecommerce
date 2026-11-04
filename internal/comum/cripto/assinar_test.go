package cripto_test

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"

	"ecommerce/internal/comum/cripto"
	"ecommerce/internal/comum/evento"
)

var (
	umaVez     sync.Once
	chaveTeste *rsa.PrivateKey
)

func chave(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	umaVez.Do(func() {
		var err error
		chaveTeste, err = cripto.GerarPar()
		if err != nil {
			panic(err)
		}
	})
	return chaveTeste
}

func envelopeDeTeste(t *testing.T) evento.Envelope {
	t.Helper()
	env, err := evento.NovoEnvelope(evento.PedidoCriado, evento.ServicoPrincipal, evento.DadosPedidoCriado{
		PedidoID: "PED-001",
		Cliente:  "Daniel",
		Itens: []evento.ItemPedido{
			{ProdutoID: "P001", Nome: "Notebook Gamer 16GB", Quantidade: 1, PrecoUnit: 4500},
			{ProdutoID: "P007", Nome: "Camiseta algodão", Quantidade: 3, PrecoUnit: 79.90},
		},
		Total: 4739.70,
	})
	if err != nil {
		t.Fatalf("montando envelope: %v", err)
	}
	return env
}

func conferir(t *testing.T, publica *rsa.PublicKey, env evento.Envelope) error {
	t.Helper()

	assinatura, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		t.Fatalf("assinatura não é base64 válido: %v", err)
	}
	conteudo, err := env.BytesCanonicos()
	if err != nil {
		t.Fatalf("bytes canônicos: %v", err)
	}
	resumo := sha256.Sum256(conteudo)
	return rsa.VerifyPKCS1v15(publica, crypto.SHA256, resumo[:], assinatura)
}

func TestAssinaturaDeEventoEValida(t *testing.T) {
	privada := chave(t)
	env := envelopeDeTeste(t)

	if err := cripto.AssinarEnvelope(privada, &env); err != nil {
		t.Fatalf("assinando: %v", err)
	}
	if env.Signature == "" {
		t.Fatal("o campo Signature ficou vazio")
	}
	if err := conferir(t, &privada.PublicKey, env); err != nil {
		t.Fatalf("a assinatura recém-gerada não foi aceita: %v", err)
	}
}

func TestAssinaturaSobreviveAoTransporteJSON(t *testing.T) {
	privada := chave(t)
	env := envelopeDeTeste(t)

	if err := cripto.AssinarEnvelope(privada, &env); err != nil {
		t.Fatalf("assinando: %v", err)
	}

	antes, err := env.BytesCanonicos()
	if err != nil {
		t.Fatalf("bytes canônicos do produtor: %v", err)
	}

	corpo, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("serializando envelope: %v", err)
	}
	var recebido evento.Envelope
	if err := json.Unmarshal(corpo, &recebido); err != nil {
		t.Fatalf("desserializando envelope: %v", err)
	}

	depois, err := recebido.BytesCanonicos()
	if err != nil {
		t.Fatalf("bytes canônicos do consumidor: %v", err)
	}

	if !bytes.Equal(antes, depois) {
		t.Fatalf("os bytes assinados mudaram no transporte\n produtor:  %s\n consumidor: %s", antes, depois)
	}
	if err := conferir(t, &privada.PublicKey, recebido); err != nil {
		t.Fatalf("assinatura recusada depois da ida e volta pelo JSON: %v", err)
	}
}

func TestAssinaturaRecusaEventoAlterado(t *testing.T) {
	privada := chave(t)

	casos := map[string]func(*evento.Envelope){
		"payload adulterado": func(e *evento.Envelope) {
			e.Dados = json.RawMessage(`{"pedido_id":"PED-001","cliente":"invasor","itens":[],"total":0}`)
		},
		"produtor trocado": func(e *evento.Envelope) {
			e.Produtor = evento.ServicoPagamento
		},
		"routing key trocada": func(e *evento.Envelope) {
			e.Tipo = evento.PagamentoAprovado
		},
		"id trocado": func(e *evento.Envelope) {
			e.ID = "0000000000000000"
		},
	}

	for nome, adulterar := range casos {
		t.Run(nome, func(t *testing.T) {
			env := envelopeDeTeste(t)
			if err := cripto.AssinarEnvelope(privada, &env); err != nil {
				t.Fatalf("assinando: %v", err)
			}

			adulterar(&env)

			if err := conferir(t, &privada.PublicKey, env); err == nil {
				t.Fatal("a assinatura foi aceita mesmo com o evento alterado")
			}
		})
	}
}

func TestAssinaturaRecusaChavePublicaDeOutroServico(t *testing.T) {
	privada := chave(t)
	env := envelopeDeTeste(t)

	if err := cripto.AssinarEnvelope(privada, &env); err != nil {
		t.Fatalf("assinando: %v", err)
	}

	outra, err := cripto.GerarPar()
	if err != nil {
		t.Fatalf("gerando segunda chave: %v", err)
	}

	if err := conferir(t, &outra.PublicKey, env); err == nil {
		t.Fatal("a assinatura foi aceita com a chave pública de outro serviço")
	}
}

func TestAssinarSemChaveDaErro(t *testing.T) {
	if _, err := cripto.Assinar(nil, []byte("qualquer coisa")); err == nil {
		t.Fatal("assinar sem chave privada deveria falhar")
	}
}
