package cripto

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"ecommerce/internal/comum/evento"
)

// Assinar gera o hash SHA-256 do conteúdo e o assina com a chave privada,
// no esquema RSASSA-PKCS#1 v1.5. Devolve a assinatura em base64.
func Assinar(privada *rsa.PrivateKey, conteudo []byte) (string, error) {
	if privada == nil {
		return "", errors.New("chave privada não carregada")
	}

	resumo := sha256.Sum256(conteudo)

	assinatura, err := rsa.SignPKCS1v15(rand.Reader, privada, crypto.SHA256, resumo[:])
	if err != nil {
		return "", fmt.Errorf("assinando evento: %w", err)
	}

	return base64.StdEncoding.EncodeToString(assinatura), nil
}

// AssinarEnvelope assina os bytes canônicos do evento e guarda o resultado
// no campo Signature do próprio envelope.
func AssinarEnvelope(privada *rsa.PrivateKey, env *evento.Envelope) error {
	conteudo, err := env.BytesCanonicos()
	if err != nil {
		return err
	}

	assinatura, err := Assinar(privada, conteudo)
	if err != nil {
		return err
	}

	env.Signature = assinatura
	return nil
}
