package cripto

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"ecommerce/internal/comum/evento"
)

// Verificar confere se assinaturaBase64 foi produzida pela chave privada
// correspondente a publica, sobre o conteúdo informado. Devolve nil quando a
// assinatura é válida.
func Verificar(publica *rsa.PublicKey, conteudo []byte, assinaturaBase64 string) error {
	if publica == nil {
		return errors.New("chave pública não carregada")
	}
	if assinaturaBase64 == "" {
		return errors.New("evento sem assinatura")
	}

	assinaturaBytes, err := base64.StdEncoding.DecodeString(assinaturaBase64)

	if err != nil {
		return fmt.Errorf("falha ao decodificar assinatura: %w", err)
	}

	resumo := sha256.Sum256(conteudo)

	if err := rsa.VerifyPKCS1v15(publica, crypto.SHA256, resumo[:], assinaturaBytes); err != nil {
		return fmt.Errorf("falha ao verificar assinatura: %w", err)
	}

	return nil
}

// VerificarEnvelope valida a assinatura de um evento recebido com a chave
// pública do produtor declarado no envelope.
func VerificarEnvelope(chaveiro *Chaveiro, env evento.Envelope) error {
	publica, err := chaveiro.PublicaDe(env.Produtor)
	if err != nil {
		return err
	}

	conteudo, err := env.BytesCanonicos()
	if err != nil {
		return err
	}

	return Verificar(publica, conteudo, env.Signature)
}
