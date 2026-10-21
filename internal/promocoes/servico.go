// Package promocoes implementa o microsserviço de promoções.
//
// Consome interesse.promocao e interesse.cancelado para manter, no SQLite, os
// e-mails inscritos em cada categoria. A cada intervalo gera uma promoção de
// um produto das categorias que têm inscritos, publica promocao.criada para o
// Estoque passar a cobrar o preço promocional e envia um e-mail a cada
// inscrito da categoria pela API de e-mail.
package promocoes

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"ecommerce/internal/catalogo"
	"ecommerce/internal/evento"
)

// Publicador é o pedaço da mensageria que o Promoções usa.
type Publicador interface {
	PublicarECommerce(ctx context.Context, routingKey string, dados any) error
}

const (
	DESCONTO_MIN                = 5
	DESCONTO_MAX                = 45
	DESCONTO_VALIDADE_MAX_HORAS = 10
)

// Notificador envia um e-mail.
type Notificador interface {
	Enviar(ctx context.Context, para, assunto, corpoHTML string) error
}

// Promocao é um desconto temporário num produto.
type Promocao struct {
	ProdutoID string
	Nome      string
	Categoria string
	PrecoDe   float64
	PrecoPor  float64
	Desconto  int
	ValidaAte time.Time
}

// Servico guarda as inscrições e dispara as promoções.
type Servico struct {
	repositorio *Repositorio
	notificador Notificador
	publicador  Publicador
	intervalo   time.Duration
}

// NovoServico monta o serviço, que gera uma promoção a cada intervalo.
func NovoServico(repositorio *Repositorio, notificador Notificador, publicador Publicador, intervalo time.Duration) *Servico {
	return &Servico{repositorio: repositorio, notificador: notificador, publicador: publicador, intervalo: intervalo}
}

// Tratar é o ponto de entrada do consumidor: despacha pela routing key.
func (s *Servico) Tratar(ctx context.Context, env evento.Envelope) error {
	switch env.Tipo {
	case evento.InteressePromocao:
		return s.aoInteresse(ctx, env)
	case evento.InteresseCancelado:
		return s.aoCancelamento(ctx, env)
	default:
		log.Printf("evento %s ignorado: não é assunto do Promoções", env.Tipo)
		return nil
	}
}

func (s *Servico) aoInteresse(ctx context.Context, env evento.Envelope) error {
	var dados evento.DadosInteressePromocao
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}
	if dados.Email == "" || len(dados.Categorias) == 0 {
		return fmt.Errorf("interesse sem e-mail ou sem categorias: %+v", dados)
	}

	if err := s.repositorio.Inscrever(ctx, dados.Email, dados.Categorias); err != nil {
		return err
	}
	log.Printf("%s inscrito nas categorias %v", dados.Email, dados.Categorias)
	return nil
}

func (s *Servico) aoCancelamento(ctx context.Context, env evento.Envelope) error {
	var dados evento.DadosInteresseCancelado
	if err := env.DecodificarDados(&dados); err != nil {
		return err
	}

	removidas, err := s.repositorio.Cancelar(ctx, dados.Email)
	switch {
	case err != nil:
		return err
	case removidas == 0:
		log.Printf("cancelamento de %s ignorado: não estava inscrito", dados.Email)
	default:
		log.Printf("inscrição de %s cancelada (%d categoria(s))", dados.Email, removidas)
	}
	return nil
}

// EnviarPromocoes gera uma promoção a cada intervalo, até o contexto acabar.
// Uma rodada que falha é registrada no log e não interrompe as próximas.
func (s *Servico) EnviarPromocoes(ctx context.Context) error {
	log.Printf("gerando uma promoção a cada %s", s.intervalo)
	relogio := time.NewTicker(s.intervalo)
	defer relogio.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-relogio.C:
			if err := s.Promover(ctx); err != nil {
				log.Printf("rodada de promoção com falha: %v", err)
			}
		}
	}
}

// Promover gera uma promoção de um produto das categorias com inscritos,
// publica promocao.criada e envia um e-mail a cada inscrito da categoria. Sem
// inscritos, não gera nada. Se a publicação falhar, nenhum e-mail sai, para a
// loja não anunciar um preço que não vai cobrar. A falha no envio para um
// e-mail não impede o envio para os demais.
func (s *Servico) Promover(ctx context.Context) error {
	categorias, err := s.repositorio.CategoriasComInscritos(ctx)
	if err != nil {
		return err
	}

	promocao, gerada := gerarPromocao(categorias, time.Now())
	if !gerada {
		log.Printf("nenhum e-mail inscrito em categorias com produtos: nenhuma promoção gerada")
		return nil
	}

	err = s.publicador.PublicarECommerce(ctx, evento.PromocaoCriada, evento.DadosPromocaoCriada{
		ProdutoID:        promocao.ProdutoID,
		PrecoPromocional: promocao.PrecoPor,
		Desconto:         promocao.Desconto,
		ValidaAte:        promocao.ValidaAte,
	})
	if err != nil {
		return fmt.Errorf("publicando a promoção de %s (nenhum e-mail enviado): %w", promocao.Nome, err)
	}

	inscritos, err := s.repositorio.Inscritos(ctx, promocao.Categoria)
	if err != nil {
		return err
	}

	log.Printf("promoção: %s (categoria %s) de %s por %s, para %d inscrito(s)",
		promocao.Nome, promocao.Categoria, moeda(promocao.PrecoDe), moeda(promocao.PrecoPor), len(inscritos))

	assunto, corpo := montarEmail(promocao)
	var falhas []error
	for _, email := range inscritos {
		if err := s.notificador.Enviar(ctx, email, assunto, corpo); err != nil {
			falhas = append(falhas, fmt.Errorf("%s: %w", email, err))
			continue
		}
		log.Printf("e-mail da promoção enviado para %s", email)
	}
	return errors.Join(falhas...)
}

// gerarPromocao sorteia um produto das categorias informadas e um desconto,
// com o preço promocional arredondado para centavos. O segundo retorno é falso
// quando nenhuma das categorias tem produto.
func gerarPromocao(categorias []string, agora time.Time) (Promocao, bool) {
	var candidatos []catalogo.Produto
	for _, categoria := range categorias {
		candidatos = append(candidatos, catalogo.PorCategoria(categoria)...)
	}
	if len(candidatos) == 0 {
		return Promocao{}, false
	}

	produto := candidatos[rand.IntN(len(candidatos))]
	desconto := DESCONTO_MIN + rand.IntN(DESCONTO_MAX-DESCONTO_MIN)
	validade := time.Duration(1+rand.IntN(DESCONTO_VALIDADE_MAX_HORAS-1)) * time.Hour

	return Promocao{
		ProdutoID: produto.ID,
		Nome:      produto.Nome,
		Categoria: produto.Categoria,
		PrecoDe:   produto.Preco,
		PrecoPor:  math.Round(produto.Preco*float64(100-desconto)) / 100,
		Desconto:  desconto,
		ValidaAte: agora.Add(validade),
	}, true
}

// montarEmail devolve o assunto e o corpo HTML do e-mail da promoção.
func montarEmail(p Promocao) (string, string) {
	assunto := fmt.Sprintf("Promoção: %s com %d%% de desconto", p.Nome, p.Desconto)
	corpo := fmt.Sprintf(`<h2>%s</h2>
<p>De <s>%s</s> por <strong>%s</strong> (%d%% de desconto).</p>
<p>Válida até %s.</p>
<p style="color:#6b7785;font-size:12px">Você recebe este e-mail porque se inscreveu nas promoções da categoria %s.</p>`,
		html.EscapeString(p.Nome), moeda(p.PrecoDe), moeda(p.PrecoPor), p.Desconto,
		p.ValidaAte.Format("02/01/2006 15:04"), p.Categoria)
	return assunto, corpo
}

// moeda formata no padrão brasileiro: R$ 4.500,00.
func moeda(valor float64) string {
	texto := strconv.FormatFloat(valor, 'f', 2, 64)
	inteiro, centavos, _ := strings.Cut(texto, ".")

	var grupos []string
	for len(inteiro) > 3 {
		grupos = append([]string{inteiro[len(inteiro)-3:]}, grupos...)
		inteiro = inteiro[:len(inteiro)-3]
	}
	grupos = append([]string{inteiro}, grupos...)

	return "R$ " + strings.Join(grupos, ".") + "," + centavos
}
