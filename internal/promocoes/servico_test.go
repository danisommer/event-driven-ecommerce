package promocoes

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ecommerce/internal/catalogo"
	"ecommerce/internal/evento"
)

type emailEnviado struct {
	para    string
	assunto string
}

type notificadorFalso struct {
	enviados   []emailEnviado
	falharPara string
}

func (n *notificadorFalso) Enviar(_ context.Context, para, assunto, _ string) error {
	n.enviados = append(n.enviados, emailEnviado{para: para, assunto: assunto})
	if para == n.falharPara {
		return errors.New("destinatário recusado")
	}
	return nil
}

type publicado struct {
	chave string
	dados any
}

type publicadorFalso struct {
	publicados []publicado
	falhar     bool
}

func (p *publicadorFalso) PublicarECommerce(_ context.Context, chave string, dados any) error {
	if p.falhar {
		return errors.New("broker fora do ar")
	}
	p.publicados = append(p.publicados, publicado{chave: chave, dados: dados})
	return nil
}

func abrirBanco(t *testing.T, caminho string) *Repositorio {
	t.Helper()
	repositorio, err := AbrirRepositorio(caminho)
	if err != nil {
		t.Fatalf("abrindo banco de teste: %v", err)
	}
	t.Cleanup(func() { repositorio.Fechar() })
	return repositorio
}

func montar(t *testing.T) (*Servico, *Repositorio, *notificadorFalso, *publicadorFalso) {
	t.Helper()
	repositorio := abrirBanco(t, filepath.Join(t.TempDir(), "promocoes.db"))
	notificador := &notificadorFalso{}
	publicador := &publicadorFalso{}
	return NovoServico(repositorio, notificador, publicador, time.Minute), repositorio, notificador, publicador
}

func tratar(t *testing.T, s *Servico, tipo string, dados any) {
	t.Helper()
	env, err := evento.NovoEnvelope(tipo, evento.ServicoPrincipal, dados)
	if err != nil {
		t.Fatalf("montando %s: %v", tipo, err)
	}
	if err := s.Tratar(context.Background(), env); err != nil {
		t.Fatalf("tratando %s: %v", tipo, err)
	}
}

func inscrever(t *testing.T, s *Servico, email string, categorias ...string) {
	t.Helper()
	tratar(t, s, evento.InteressePromocao, evento.DadosInteressePromocao{Email: email, Categorias: categorias})
}

func exigirInscritos(t *testing.T, repositorio *Repositorio, categoria string, esperados ...string) {
	t.Helper()
	inscritos, err := repositorio.Inscritos(context.Background(), categoria)
	if err != nil {
		t.Fatalf("consultando inscritos: %v", err)
	}
	if !slices.Equal(inscritos, append([]string{}, esperados...)) {
		t.Fatalf("inscritos em %s: esperava %v, veio %v", categoria, esperados, inscritos)
	}
}

func TestInteresseNovoSubstituiAsCategorias(t *testing.T) {
	s, repositorio, _, _ := montar(t)

	inscrever(t, s, "ana@exemplo.com", "A", "B")
	inscrever(t, s, "ana@exemplo.com", "C")

	exigirInscritos(t, repositorio, "A")
	exigirInscritos(t, repositorio, "B")
	exigirInscritos(t, repositorio, "C", "ana@exemplo.com")
}

func TestCancelamentoRemoveTodasAsCategorias(t *testing.T) {
	s, repositorio, _, _ := montar(t)
	inscrever(t, s, "ana@exemplo.com", "A", "B")

	tratar(t, s, evento.InteresseCancelado, evento.DadosInteresseCancelado{Email: "ana@exemplo.com"})
	tratar(t, s, evento.InteresseCancelado, evento.DadosInteresseCancelado{Email: "nunca-inscrito@exemplo.com"})

	exigirInscritos(t, repositorio, "A")
	exigirInscritos(t, repositorio, "B")
}

func TestInteresseSemCategoriasEDescartado(t *testing.T) {
	s, _, _, _ := montar(t)

	env, err := evento.NovoEnvelope(evento.InteressePromocao, evento.ServicoPrincipal,
		evento.DadosInteressePromocao{Email: "ana@exemplo.com"})
	if err != nil {
		t.Fatalf("montando envelope: %v", err)
	}

	if err := s.Tratar(context.Background(), env); err == nil {
		t.Fatal("um interesse sem categorias deveria ser recusado")
	}
}

func TestInscricoesSobrevivemAReabrirOBanco(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "promocoes.db")

	primeiro, err := AbrirRepositorio(caminho)
	if err != nil {
		t.Fatalf("abrindo banco: %v", err)
	}
	inscrever(t, NovoServico(primeiro, &notificadorFalso{}, &publicadorFalso{}, time.Minute), "ana@exemplo.com", "B")
	primeiro.Fechar()

	exigirInscritos(t, abrirBanco(t, caminho), "B", "ana@exemplo.com")
}

func TestPromocaoVaiSoParaOsInscritosDaCategoria(t *testing.T) {
	s, _, notificador, _ := montar(t)
	inscrever(t, s, "ana@exemplo.com", "A")
	inscrever(t, s, "bruno@exemplo.com", "A", "C")

	if err := s.Promover(context.Background()); err != nil {
		t.Fatalf("promovendo: %v", err)
	}

	var destinatarios []string
	for _, e := range notificador.enviados {
		destinatarios = append(destinatarios, e.para)
		if !strings.HasPrefix(e.assunto, "Promoção: ") {
			t.Fatalf("assunto inesperado: %q", e.assunto)
		}
	}

	switch {
	case slices.Equal(destinatarios, []string{"ana@exemplo.com", "bruno@exemplo.com"}):
	case slices.Equal(destinatarios, []string{"bruno@exemplo.com"}):
	default:
		t.Fatalf("a promoção foi para quem não está inscrito na categoria dela: %v", destinatarios)
	}
}

func TestSemInscritosNaoGeraPromocao(t *testing.T) {
	s, _, notificador, _ := montar(t)

	if err := s.Promover(context.Background()); err != nil {
		t.Fatalf("promovendo: %v", err)
	}
	if len(notificador.enviados) != 0 {
		t.Fatalf("sem inscritos, nenhum e-mail deveria sair, saíram %d", len(notificador.enviados))
	}
}

func TestFalhaNumEmailNaoImpedeOsOutros(t *testing.T) {
	s, _, notificador, _ := montar(t)
	notificador.falharPara = "ana@exemplo.com"
	inscrever(t, s, "ana@exemplo.com", "B")
	inscrever(t, s, "bruno@exemplo.com", "B")

	err := s.Promover(context.Background())

	if err == nil || !strings.Contains(err.Error(), "ana@exemplo.com") {
		t.Fatalf("esperava erro apontando ana@exemplo.com, veio %v", err)
	}
	if len(notificador.enviados) != 2 {
		t.Fatalf("os dois inscritos deveriam ter sido tentados, foram %d", len(notificador.enviados))
	}
}

func TestPromocaoEPublicadaParaOEstoque(t *testing.T) {
	s, _, notificador, publicador := montar(t)
	inscrever(t, s, "ana@exemplo.com", "B")

	if err := s.Promover(context.Background()); err != nil {
		t.Fatalf("promovendo: %v", err)
	}

	if len(publicador.publicados) != 1 || publicador.publicados[0].chave != evento.PromocaoCriada {
		t.Fatalf("esperava publicar %s, publicou %+v", evento.PromocaoCriada, publicador.publicados)
	}
	dados := publicador.publicados[0].dados.(evento.DadosPromocaoCriada)
	produto, existe := catalogo.Buscar(dados.ProdutoID)
	switch {
	case !existe || produto.Categoria != "B":
		t.Fatalf("promoção de produto fora da categoria inscrita: %+v", dados)
	case dados.PrecoPromocional <= 0 || dados.PrecoPromocional >= produto.Preco:
		t.Fatalf("preço promocional inesperado: %.2f (tabela %.2f)", dados.PrecoPromocional, produto.Preco)
	case !dados.ValidaAte.After(time.Now()):
		t.Fatalf("validade no passado: %v", dados.ValidaAte)
	}
	if len(notificador.enviados) != 1 {
		t.Fatalf("esperava um e-mail, saíram %d", len(notificador.enviados))
	}
}

func TestFalhaAoPublicarAPromocaoNaoEnviaEmail(t *testing.T) {
	s, _, notificador, publicador := montar(t)
	publicador.falhar = true
	inscrever(t, s, "ana@exemplo.com", "B")

	if err := s.Promover(context.Background()); err == nil {
		t.Fatal("com o broker fora do ar, Promover deveria devolver erro")
	}
	if len(notificador.enviados) != 0 {
		t.Fatalf("sem a promoção registrada no Estoque, nenhum e-mail deveria sair, saíram %d", len(notificador.enviados))
	}
}

func TestGerarPromocaoAplicaODescontoNaCategoriaPedida(t *testing.T) {
	agora := time.Now()

	for i := 0; i < 50; i++ {
		promocao, gerada := gerarPromocao([]string{"C"}, agora)
		if !gerada {
			t.Fatal("a categoria C tem produtos, a promoção deveria ser gerada")
		}

		produto, _ := catalogo.Buscar(promocao.ProdutoID)
		semArredondar := produto.Preco * float64(100-promocao.Desconto) / 100
		centavos := promocao.PrecoPor * 100
		switch {
		case promocao.Categoria != "C":
			t.Fatalf("promoção fora da categoria pedida: %+v", promocao)
		case promocao.Desconto < DESCONTO_MIN || promocao.Desconto >= DESCONTO_MAX:
			t.Fatalf("desconto fora da faixa: %d", promocao.Desconto)
		case math.Abs(promocao.PrecoPor-semArredondar) > 0.005:
			t.Fatalf("preço com desconto errado: %.4f, esperava perto de %.4f", promocao.PrecoPor, semArredondar)
		case math.Abs(centavos-math.Round(centavos)) > 1e-6:
			t.Fatalf("preço promocional com mais de dois decimais: %v", promocao.PrecoPor)
		case !promocao.ValidaAte.After(agora):
			t.Fatalf("validade no passado: %v", promocao.ValidaAte)
		}
	}

	if _, gerada := gerarPromocao(nil, agora); gerada {
		t.Fatal("sem categorias, nenhuma promoção deveria ser gerada")
	}
}
