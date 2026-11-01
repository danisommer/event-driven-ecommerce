"""Grava a demonstração da loja de ponta a ponta com o Playwright.

Com os processos no ar (docs/demo/servicos.sh subir):

    docs/demo/.venv/bin/python docs/demo/gravar.py

Grava um vídeo por aba em docs/demo/execucao/bruto/ e, na mesma pasta, o
linha_do_tempo.json com o início de cada aba e o instante de cada cena, que o
pos_producao.py usa para editar, junto com os saltos do relógio de parede
durante a gravação. Cada aba termina com um pulso magenta, fora das cenas, que
a pós-produção usa para alinhar as marcas ao vídeo. LOJA_URL, GATEWAY_URL e
PAINEL_URL trocam os endereços.
"""

import json
import math
import os
import random
import threading
import time
from pathlib import Path

from playwright.sync_api import BrowserContext, Locator, Page, expect, sync_playwright

LARGURA, ALTURA = 1280, 720
LOJA_URL = os.environ.get("LOJA_URL", "http://localhost:5173")
GATEWAY_URL = os.environ.get("GATEWAY_URL", "http://localhost:8080")
PAINEL_URL = os.environ.get("PAINEL_URL", "http://localhost:15672")
BRUTO = Path(__file__).resolve().parent / "execucao" / "bruto"

EMAIL = "maria@example.com"
NOME = "Maria"
CATEGORIA_PROMOCAO = "C"
TEMPO_LIMITE_MS = 15_000
TEMPO_LIMITE_PROMOCAO_S = 90
MARGEM_INFERIOR = 130
AVISOS_SOMEM_S = 7.2
PULSO_MS = 500
LIMIAR_DE_SALTO_S = 0.2

LEGENDAS = {
    "catalogo": "Catalog served by the Inventory service through the API Gateway",
    "inscricao": "Subscribing to promotion emails for one product category",
    "pedido": "Two items in the cart: the order is accepted and processed asynchronously",
    "aprovado": "Payment approved through the external provider's webhook",
    "sse": "Order status updates in real time via SSE",
    "recusado": "Second order: a declined payment cancels it and returns the reserved stock",
    "promocao": "New promotion: the Inventory now charges the discounted price",
    "rabbitmq": "Every service talks only through RSA-signed RabbitMQ events",
}

SOBREPOSICAO_JS = r"""
(() => {
  if (window.top !== window || window.__demo) return;
  const estilo = `
    #__demo-legenda { position: fixed; left: 24px; bottom: 22px; max-width: 780px; z-index: 2147483647;
      box-sizing: border-box; padding: 10px 18px; border-radius: 10px;
      background: rgba(15, 23, 42, .86); color: #f8fafc;
      font: 600 17px/1.4 system-ui, -apple-system, "Segoe UI", sans-serif; letter-spacing: .01em;
      box-shadow: 0 8px 24px rgba(15, 23, 42, .28); pointer-events: none;
      transition: opacity .3s ease, transform .3s ease; }
    #__demo-legenda.__oculta { opacity: 0; transform: translateY(8px); }
    #__demo-legenda.__sem-transicao { transition: none; }
    #__demo-cursor { position: fixed; left: 0; top: 0; z-index: 2147483647; pointer-events: none;
      width: 26px; height: 26px; transform: translate(-60px, -60px);
      filter: drop-shadow(0 1px 2px rgba(0, 0, 0, .35)); }
    .__demo-clique { position: fixed; z-index: 2147483646; pointer-events: none;
      width: 34px; height: 34px; margin: -17px 0 0 -17px; border-radius: 50%; box-sizing: border-box;
      border: 3px solid rgba(37, 99, 235, .75); animation: __demo-onda .5s ease-out forwards; }
    @keyframes __demo-onda { from { transform: scale(.3); opacity: 1; } to { transform: scale(1.5); opacity: 0; } }
    #__demo-pulso { position: fixed; inset: 0; z-index: 2147483647; background: #ff00ff; pointer-events: none; }
  `;
  const seta = '<svg viewBox="0 0 26 26" width="26" height="26"><path d="M4 2.5v18.2l4.6-4.4 3.2 7.1 3-1.3-3.1-6.9h6.4z" fill="#111827" stroke="#fff" stroke-width="1.6" stroke-linejoin="round"/></svg>';
  let legenda;
  let cursor;

  const posicionar = (x, y) => {
    cursor.style.transform = `translate(${x - 4}px, ${y - 2.5}px)`;
  };

  const trocarTexto = (texto) => {
    legenda.textContent = texto;
    legenda.classList.toggle("__oculta", !texto);
  };

  const restaurar = (texto) => {
    legenda.classList.add("__sem-transicao");
    trocarTexto(texto);
    legenda.getBoundingClientRect();
    legenda.classList.remove("__sem-transicao");
  };

  const legendar = (texto) => {
    legenda.classList.add("__oculta");
    setTimeout(() => trocarTexto(texto), 300);
  };

  const pulsar = (ms) => new Promise((pronto) => {
    const pulso = document.createElement("div");
    pulso.id = "__demo-pulso";
    document.documentElement.append(pulso);
    setTimeout(() => {
      pulso.remove();
      pronto();
    }, ms);
  });

  const ondaDeClique = (evento) => {
    const onda = document.createElement("div");
    onda.className = "__demo-clique";
    onda.style.left = `${evento.clientX}px`;
    onda.style.top = `${evento.clientY}px`;
    document.documentElement.append(onda);
    setTimeout(() => onda.remove(), 600);
  };

  const montar = () => {
    const css = document.createElement("style");
    css.textContent = estilo;
    legenda = document.createElement("div");
    legenda.id = "__demo-legenda";
    legenda.className = "__oculta";
    cursor = document.createElement("div");
    cursor.id = "__demo-cursor";
    cursor.innerHTML = seta;
    document.documentElement.append(css, legenda, cursor);
    addEventListener("mousemove", (evento) => posicionar(evento.clientX, evento.clientY), { capture: true, passive: true });
    addEventListener("mousedown", ondaDeClique, { capture: true, passive: true });
    window.__demoEstado().then(({ legenda: texto, x, y }) => {
      posicionar(x, y);
      restaurar(texto);
    });
  };

  window.__demo = { legendar, pulsar };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", montar, { once: true });
  } else {
    montar();
  }
})();
"""

ROLAR_JS = r"""
([delta]) => new Promise((pronto) => {
  if (Math.abs(delta) < 2) return pronto();
  scrollBy({ top: delta, behavior: "smooth" });
  let ultimo = -1;
  let parado = 0;
  const conferir = () => {
    if (scrollY === ultimo) {
      if (++parado > 6) return pronto();
    } else {
      parado = 0;
      ultimo = scrollY;
    }
    requestAnimationFrame(conferir);
  };
  requestAnimationFrame(conferir);
})
"""


def suavizar(fracao: float) -> float:
    """Curva de aceleração e desaceleração do movimento do mouse."""
    if fracao < 0.5:
        return 4 * fracao**3
    return 1 - (-2 * fracao + 2) ** 3 / 2


class Relogio:
    """Mede o tempo da gravação no relógio monotônico e anota cada salto do
    relógio de parede, que o vídeo do Playwright acompanha."""

    def __init__(self) -> None:
        self.t0 = time.monotonic()
        self.saltos: list[dict] = []
        self._parar = threading.Event()
        self._vigia = threading.Thread(target=self._vigiar, daemon=True)

    def agora(self) -> float:
        return round(time.monotonic() - self.t0, 3)

    def _vigiar(self) -> None:
        anterior = time.time() - time.monotonic()
        while not self._parar.wait(0.02):
            atual = time.time() - time.monotonic()
            if abs(atual - anterior) > LIMIAR_DE_SALTO_S:
                self.saltos.append({"instante": self.agora(), "segundos": round(atual - anterior, 3)})
            anterior = atual

    def __enter__(self) -> "Relogio":
        self._vigia.start()
        return self

    def __exit__(self, *_) -> None:
        self._parar.set()
        self._vigia.join()


class Diretor:
    """Conduz as abas em ritmo humano, mantém a legenda e o cursor desenhados
    em cada aba e anota o instante de cada cena para a edição."""

    def __init__(self, contexto: BrowserContext, relogio: Relogio) -> None:
        self.contexto = contexto
        self.relogio = relogio
        self.criadas: dict[Page, float] = {}
        self.abas: dict[str, Page] = {}
        self.marcas: dict[str, float] = {}
        self.legenda = ""
        self.legendas: dict[Page, str] = {}
        self.cursor = (LARGURA * 0.62, ALTURA * 0.45)
        self.cursores: dict[Page, tuple[float, float]] = {}
        self.sorteio = random.Random(20261031)
        contexto.on("page", self._nova_aba)
        contexto.expose_binding("__demoEstado", self._estado)
        contexto.add_init_script(script=SOBREPOSICAO_JS)

    def agora(self) -> float:
        return self.relogio.agora()

    def _nova_aba(self, pagina: Page, criada: float | None = None) -> None:
        self.criadas.setdefault(pagina, self.agora() if criada is None else criada)

    def _estado(self, fonte: dict) -> dict:
        pagina = fonte["page"]
        x, y = self.cursores.get(pagina, self.cursor)
        return {"legenda": self.legendas.get(pagina, self.legenda), "x": x, "y": y}

    def marcar(self, nome: str) -> None:
        """Anota o instante de uma cena. Nome repetido é erro do roteiro."""
        if nome in self.marcas:
            raise ValueError(f"marca repetida: {nome}")
        self.marcas[nome] = self.agora()

    def registrar(self, nome: str, pagina: Page) -> Page:
        """Dá nome a uma aba; o vídeo dela começa quando ela foi criada."""
        if pagina not in self.criadas:
            raise RuntimeError(f"a aba {nome} não passou pelo evento de página nova")
        self.abas[nome] = pagina
        return pagina

    def abrir(self, nome: str) -> Page:
        antes = self.agora()
        pagina = self.contexto.new_page()
        self._nova_aba(pagina, antes)
        return self.registrar(nome, pagina)

    def esperar_ate(self, pagina: Page, instante: float) -> None:
        restante_ms = (instante - self.agora()) * 1000
        if restante_ms > 0:
            pagina.wait_for_timeout(restante_ms)

    def sincronizar(self, nome: str) -> None:
        """Pisca a aba em magenta e anota `<nome>_sincronia`, para alinhar o vídeo na edição."""
        pagina = self.abas[nome]
        pagina.wait_for_timeout(300)
        self.marcar(f"{nome}_sincronia")
        pagina.evaluate("ms => window.__demo.pulsar(ms)", PULSO_MS)
        pagina.wait_for_timeout(300)

    def legendar(self, texto: str, *paginas: Page) -> None:
        """Troca a legenda das abas informadas e a que as próximas abas recebem."""
        self.legenda = texto
        for pagina in paginas:
            self.legendas[pagina] = texto
            pagina.evaluate("texto => window.__demo.legendar(texto)", texto)

    def esperar(self, pagina: Page, ms: int) -> None:
        pagina.wait_for_timeout(ms)

    def rolar(self, pagina: Page, delta: float) -> None:
        pagina.evaluate(ROLAR_JS, [delta])

    def rolar_ate(self, pagina: Page, alvo: Locator, topo: float) -> None:
        """Rola suavemente até o alvo ficar a `topo` pixels do alto da janela."""
        caixa = alvo.bounding_box()
        self.rolar(pagina, caixa["y"] - topo)

    def rolar_ao_inicio(self, pagina: Page) -> None:
        self.rolar(pagina, -pagina.evaluate("scrollY"))

    def mostrar(self, pagina: Page, alvo: Locator, topo: float = 70, margem_inferior: float = MARGEM_INFERIOR) -> None:
        """Rola o mínimo para o alvo caber entre o topo e a faixa da legenda."""
        caixa = alvo.bounding_box()
        base = ALTURA - margem_inferior
        excesso = caixa["y"] + caixa["height"] - base
        delta = max(excesso, 0) if caixa["y"] - max(excesso, 0) >= topo else caixa["y"] - topo
        self.rolar(pagina, delta)

    def mover(self, pagina: Page, x: float, y: float) -> None:
        """Leva o mouse até (x, y) num arco suave, na velocidade de uma mão."""
        x0, y0 = self.cursores.get(pagina, self.cursor)
        distancia = math.hypot(x - x0, y - y0)
        duracao_ms = min(900, 280 + distancia * 0.55)
        passos = max(8, int(duracao_ms / 16))
        curva = distancia * 0.06 * self.sorteio.choice((-1, 1))
        nx, ny = ((y0 - y) / distancia, (x - x0) / distancia) if distancia else (0, 0)
        for passo in range(1, passos + 1):
            fracao = suavizar(passo / passos)
            arco = math.sin(math.pi * fracao) * curva
            pagina.mouse.move(x0 + (x - x0) * fracao + nx * arco, y0 + (y - y0) * fracao + ny * arco)
            pagina.wait_for_timeout(duracao_ms / passos)
        self.cursores[pagina] = (x, y)
        self.cursor = (x, y)

    def apontar(self, pagina: Page, alvo: Locator, rolar: bool = True) -> None:
        """Leva o mouse até perto do centro do alvo, rolando antes se ele não estiver à vista."""
        if rolar:
            self.mostrar(pagina, alvo)
        caixa = alvo.bounding_box()
        x = caixa["x"] + caixa["width"] * self.sorteio.uniform(0.42, 0.58)
        y = caixa["y"] + caixa["height"] * self.sorteio.uniform(0.42, 0.58)
        self.mover(pagina, x, y)

    def clicar(self, pagina: Page, alvo: Locator, marca: str | None = None) -> None:
        """Aponta, hesita como uma pessoa e clica; `marca` anota o instante do clique."""
        self.apontar(pagina, alvo)
        pagina.wait_for_timeout(self.sorteio.randint(140, 240))
        if marca:
            self.marcar(marca)
        pagina.mouse.down()
        pagina.wait_for_timeout(90)
        pagina.mouse.up()
        pagina.wait_for_timeout(self.sorteio.randint(250, 400))

    def digitar(self, pagina: Page, texto: str) -> None:
        for letra in texto:
            pagina.keyboard.type(letra)
            pagina.wait_for_timeout(self.sorteio.randint(55, 135))

    def finalizar_pedido(self, loja: Page, nome_aba: str, marca: str) -> tuple[str, Page]:
        """Clica em "Finalizar pedido" e devolve o id do pedido e a aba de pagamento que ele abre."""
        def eh_criacao(resposta) -> bool:
            return resposta.request.method == "POST" and resposta.url.endswith("/pedidos")

        with loja.expect_popup() as popup, loja.expect_response(eh_criacao) as resposta:
            self.clicar(loja, loja.locator("#finalizar"), marca=marca)
        if resposta.value.status != 202:
            raise RuntimeError(f"POST /pedidos respondeu {resposta.value.status}: {resposta.value.text()}")
        return resposta.value.json()["id"], self.registrar(nome_aba, popup.value)

    def gravar_linha_do_tempo(self, destino: Path, videos: dict[str, str]) -> None:
        abas = {nome: {"video": videos[nome], "inicio": self.criadas[pagina]} for nome, pagina in self.abas.items()}
        linha = {"abas": abas, "marcas": self.marcas, "saltos_do_relogio": self.relogio.saltos}
        destino.write_text(json.dumps(linha, indent=2, ensure_ascii=False))


def filtro(loja: Page, categoria: str) -> Locator:
    return loja.locator("#filtros button").filter(has_text=f"{categoria} · ")


def produto(loja: Page, nome: str) -> Locator:
    return loja.locator("article.produto").filter(has=loja.get_by_role("heading", name=nome, exact=True))


def cartao_do_pedido(loja: Page, pedido_id: str) -> Locator:
    return loja.locator("article.pedido").filter(has_text=pedido_id)


def esperar_status(cartao: Locator, rotulo: str) -> None:
    expect(cartao.locator(".selo")).to_have_text(rotulo, timeout=TEMPO_LIMITE_MS)


def encaixar_sob_o_carrinho(d: Diretor, loja: Page, cartao: Locator) -> None:
    """Rola até o pedido ficar logo abaixo do carrinho fixo."""
    carrinho = loja.locator(".carrinho").bounding_box()
    d.rolar_ate(loja, cartao, topo=carrinho["y"] + carrinho["height"] + 14)


def cena_catalogo(d: Diretor, loja: Page) -> None:
    d.marcar("loja_abrindo")
    loja.goto(LOJA_URL)
    expect(loja.locator("article.produto")).to_have_count(9, timeout=TEMPO_LIMITE_MS)
    expect(loja.locator("#conexao")).to_have_attribute("data-estado", "conectado", timeout=TEMPO_LIMITE_MS)
    d.esperar(loja, 600)
    d.legendar(LEGENDAS["catalogo"], loja)
    d.esperar(loja, 1200)
    d.apontar(loja, produto(loja, "Air Fryer 5L").locator(".preco"))
    d.esperar(loja, 1800)


def cena_inscricao(d: Diretor, loja: Page) -> None:
    d.legendar(LEGENDAS["inscricao"], loja)
    d.rolar_ate(loja, loja.locator("#promocoes"), topo=80)
    d.esperar(loja, 400)
    d.clicar(loja, loja.locator("#email"))
    d.digitar(loja, EMAIL)
    d.esperar(loja, 300)
    d.clicar(loja, loja.locator("label.opcao").filter(has_text=f"{CATEGORIA_PROMOCAO} · "))
    d.esperar(loja, 300)
    d.clicar(loja, loja.get_by_role("button", name="Quero receber"))
    mensagem = loja.locator("#mensagem-promocoes")
    expect(mensagem).to_contain_text(EMAIL, timeout=TEMPO_LIMITE_MS)
    d.marcar("inscricao_ok")
    d.esperar(loja, 300)
    d.mostrar(loja, mensagem)
    d.esperar(loja, 2000)


def cena_pedido_aprovado(d: Diretor, loja: Page) -> None:
    d.legendar(LEGENDAS["pedido"], loja)
    d.rolar_ao_inicio(loja)
    d.esperar(loja, 300)
    d.clicar(loja, filtro(loja, "A"))
    d.esperar(loja, 400)
    d.clicar(loja, produto(loja, "Mouse sem fio").get_by_role("button"))
    d.clicar(loja, produto(loja, "Teclado mecânico").get_by_role("button"))
    d.clicar(loja, loja.locator("#nome-cliente"))
    d.digitar(loja, NOME)
    d.esperar(loja, 300)
    pedido_id, pagamento = d.finalizar_pedido(loja, "pagamento1", "pedido1_clique")

    cartao = cartao_do_pedido(loja, pedido_id)
    encaixar_sob_o_carrinho(d, loja, cartao)
    esperar_status(cartao, "Aguardando pagamento")
    d.marcar("pedido1_aguardando_pagamento")
    d.esperar(loja, 1700)
    d.marcar("pedido1_visto")
    d.legendar(LEGENDAS["sse"], loja)

    aprovar = pagamento.get_by_role("button", name="Pagamento Aprovado")
    expect(aprovar).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.esperar_ate(pagamento, d.marcas["pedido1_clique"] + AVISOS_SOMEM_S)
    d.marcar("checkout1")
    d.legendar(LEGENDAS["aprovado"], pagamento)
    d.esperar(pagamento, 1300)
    d.clicar(pagamento, aprovar, marca="aprovar1_clique")
    expect(pagamento.get_by_text("Pagamento aprovado.")).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.marcar("aprovado1_resultado")

    esperar_status(cartao, "Pedido enviado")
    d.marcar("pedido1_enviado")
    d.esperar(loja, 1200)
    d.apontar(loja, cartao.locator(".detalhe"), rolar=False)
    d.esperar(loja, 2300)
    d.marcar("pedido1_fim")
    d.sincronizar("pagamento1")
    pagamento.close()


def cena_pedido_recusado(d: Diretor, loja: Page) -> None:
    d.legendar(LEGENDAS["recusado"], loja)
    d.marcar("pedido2_inicio")
    d.rolar_ao_inicio(loja)
    d.esperar(loja, 300)
    d.clicar(loja, filtro(loja, "B"))
    air_fryer = produto(loja, "Air Fryer 5L")
    expect(air_fryer.locator(".estoque")).to_have_text("8 em estoque")
    d.apontar(loja, air_fryer.locator(".estoque"))
    d.esperar(loja, 900)
    d.clicar(loja, air_fryer.get_by_role("button"))
    d.esperar(loja, 300)
    pedido_id, pagamento = d.finalizar_pedido(loja, "pagamento2", "pedido2_clique")

    cartao = cartao_do_pedido(loja, pedido_id)
    encaixar_sob_o_carrinho(d, loja, cartao)
    esperar_status(cartao, "Aguardando pagamento")
    expect(air_fryer.locator(".estoque")).to_have_text("7 em estoque", timeout=TEMPO_LIMITE_MS)
    d.marcar("pedido2_aguardando_pagamento")
    d.apontar(loja, air_fryer.locator(".estoque"), rolar=False)
    d.esperar(loja, 1300)
    d.marcar("pedido2_visto")

    recusar = pagamento.get_by_role("button", name="Pagamento Recusado")
    expect(recusar).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.esperar_ate(pagamento, d.marcas["pedido2_clique"] + AVISOS_SOMEM_S)
    d.marcar("checkout2")
    d.esperar(pagamento, 1000)
    d.clicar(pagamento, recusar, marca="recusar2_clique")
    expect(pagamento.get_by_text("Pagamento recusado.")).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.marcar("recusado2_resultado")

    esperar_status(cartao, "Pagamento recusado")
    d.esperar(loja, 1500)
    d.apontar(loja, cartao.locator(".etapas li.falhou"), rolar=False)
    d.esperar(loja, 1500)
    d.marcar("pedido2_fim")
    d.sincronizar("pagamento2")
    pagamento.close()


def esperar_promocao(loja: Page) -> None:
    """Espera o Estoque ter ao menos um produto da categoria inscrita em promoção."""
    limite = time.monotonic() + TEMPO_LIMITE_PROMOCAO_S
    while time.monotonic() < limite:
        resposta = loja.request.get(f"{GATEWAY_URL}/produtos")
        if not resposta.ok:
            raise RuntimeError(f"GET /produtos respondeu {resposta.status}: {resposta.text()}")
        if any(p["categoria"] == CATEGORIA_PROMOCAO and p.get("preco_original", 0) > p["preco"] for p in resposta.json()):
            return
        loja.wait_for_timeout(1000)
    raise TimeoutError(f"nenhuma promoção da categoria {CATEGORIA_PROMOCAO} em {TEMPO_LIMITE_PROMOCAO_S}s")


def cena_estoque_devolvido(d: Diretor, loja: Page) -> None:
    esperar_promocao(loja)
    d.marcar("recarregar")
    loja.reload()
    expect(loja.locator("article.produto")).to_have_count(9, timeout=TEMPO_LIMITE_MS)
    d.esperar(loja, 500)
    d.rolar_ao_inicio(loja)
    d.clicar(loja, filtro(loja, "B"))
    estoque = produto(loja, "Air Fryer 5L").locator(".estoque")
    expect(estoque).to_have_text("8 em estoque", timeout=TEMPO_LIMITE_MS)
    d.apontar(loja, estoque)
    d.marcar("estoque_devolvido")
    d.esperar(loja, 2200)


def cena_promocao(d: Diretor, loja: Page) -> None:
    d.legendar(LEGENDAS["promocao"], loja)
    d.esperar(loja, 700)
    d.clicar(loja, filtro(loja, CATEGORIA_PROMOCAO))
    em_promocao = loja.locator("article.produto.em-promocao").first
    expect(em_promocao).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.apontar(loja, em_promocao.locator(".preco"))
    d.marcar("promocao_visivel")
    d.esperar(loja, 3200)
    d.marcar("promocao_fim")
    d.sincronizar("loja")


def cena_rabbitmq(d: Diretor) -> None:
    d.legendar(LEGENDAS["rabbitmq"])
    painel = d.abrir("rabbitmq")
    painel.goto(PAINEL_URL)
    painel.locator("input[name=username]").fill("guest")
    painel.locator("input[name=password]").fill("guest")
    painel.locator("input[type=submit]").click()
    painel.locator('#tabs a[href="#/exchanges"]').wait_for(timeout=TEMPO_LIMITE_MS)
    painel.goto(f"{PAINEL_URL}/#/exchanges/%2F/eCommerce")
    painel.locator("h2", has_text="Bindings").wait_for(timeout=TEMPO_LIMITE_MS)
    painel.locator("h2", has_text="Overview").click()
    painel.locator("h2", has_text="Bindings").click()
    expect(painel.get_by_text("principal.status").first).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.esperar(painel, 1000)

    d.marcar("rabbit_exchange")
    d.esperar(painel, 3000)
    d.clicar(painel, painel.locator('#tabs a[href="#/queues"]'))
    expect(painel.get_by_role("link", name="promocoes.interesses")).to_be_visible(timeout=TEMPO_LIMITE_MS)
    d.marcar("rabbit_filas")
    d.esperar(painel, 3200)
    d.marcar("rabbit_fim")
    d.sincronizar("rabbitmq")


def gravar() -> None:
    BRUTO.mkdir(parents=True, exist_ok=True)
    for antigo in [*BRUTO.glob("*.webm"), *BRUTO.glob("*.json")]:
        antigo.unlink()

    with sync_playwright() as playwright, Relogio() as relogio:
        navegador = playwright.chromium.launch()
        contexto = navegador.new_context(
            viewport={"width": LARGURA, "height": ALTURA},
            record_video_dir=str(BRUTO),
            record_video_size={"width": LARGURA, "height": ALTURA},
            device_scale_factor=1,
            color_scheme="light",
            locale="pt-BR",
        )
        d = Diretor(contexto, relogio)
        loja = d.abrir("loja")

        cena_catalogo(d, loja)
        cena_inscricao(d, loja)
        cena_pedido_aprovado(d, loja)
        cena_pedido_recusado(d, loja)
        cena_estoque_devolvido(d, loja)
        cena_promocao(d, loja)
        cena_rabbitmq(d)

        originais = {nome: Path(pagina.video.path()) for nome, pagina in d.abas.items()}
        contexto.close()
        navegador.close()

    videos = {}
    for nome, original in originais.items():
        original.replace(BRUTO / f"{nome}.webm")
        videos[nome] = f"{nome}.webm"
    d.gravar_linha_do_tempo(BRUTO / "linha_do_tempo.json", videos)
    saltos = ", ".join(f"{s['segundos']:+.2f}s em {s['instante']:.1f}s" for s in relogio.saltos) or "nenhum"
    print(f"gravado em {BRUTO}: {', '.join(videos.values())}; saltos do relógio: {saltos}")


if __name__ == "__main__":
    gravar()
