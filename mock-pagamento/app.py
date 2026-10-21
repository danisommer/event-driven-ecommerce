"""Mock de Pagamento: simula um provedor de pagamentos externo à loja.

Fala só HTTP, sem RabbitMQ e sem as chaves da loja. O MS Pagamento cria a
cobrança em POST /cobrancas informando a URL do seu webhook; o comprador abre
a página de checkout e escolhe aprovar ou recusar; o Mock então avisa o
resultado com um POST nesse webhook.
"""

import logging
import os
import secrets
import threading
from dataclasses import dataclass
from urllib.parse import urlparse

import requests
from flask import Flask, abort, jsonify, redirect, render_template, request, url_for

PENDENTE = "PENDENTE"
ENVIANDO = "ENVIANDO"
APROVADO = "APROVADO"
RECUSADO = "RECUSADO"
DECISOES = (APROVADO, RECUSADO)

TEMPO_LIMITE_WEBHOOK_SEGUNDOS = 5

registro = logging.getLogger("mock-pagamento")


@dataclass
class Cobranca:
    """Uma cobrança criada pelo MS Pagamento e o estado da decisão do comprador."""

    id: str
    pedido_id: str
    valor: float
    webhook_url: str
    status: str = PENDENTE


class Cobrancas:
    """Guarda as cobranças em memória. Toda troca de status passa pela trava,
    para dois cliques ao mesmo tempo não decidirem a mesma cobrança duas vezes."""

    def __init__(self) -> None:
        self._trava = threading.Lock()
        self._por_id: dict[str, Cobranca] = {}

    def criar(self, pedido_id: str, valor: float, webhook_url: str) -> Cobranca:
        cobranca = Cobranca(
            id="COB-" + secrets.token_hex(4),
            pedido_id=pedido_id,
            valor=valor,
            webhook_url=webhook_url,
        )
        with self._trava:
            self._por_id[cobranca.id] = cobranca
        return cobranca

    def buscar(self, cobranca_id: str) -> Cobranca | None:
        with self._trava:
            return self._por_id.get(cobranca_id)

    def iniciar_envio(self, cobranca_id: str) -> bool:
        """Marca a cobrança como em envio. Devolve False se ela não estava pendente."""
        with self._trava:
            cobranca = self._por_id[cobranca_id]
            if cobranca.status != PENDENTE:
                return False
            cobranca.status = ENVIANDO
            return True

    def concluir(self, cobranca_id: str, decisao: str) -> None:
        with self._trava:
            self._por_id[cobranca_id].status = decisao

    def reabrir(self, cobranca_id: str) -> None:
        """Volta a cobrança para pendente quando o aviso ao webhook falhou."""
        with self._trava:
            self._por_id[cobranca_id].status = PENDENTE


def validar_cobranca(corpo: object) -> str | None:
    """Devolve a mensagem de erro do corpo de POST /cobrancas, ou None se ele é válido."""
    if not isinstance(corpo, dict):
        return "o corpo deve ser um objeto JSON"

    pedido_id = corpo.get("pedido_id")
    if not isinstance(pedido_id, str) or not pedido_id.strip():
        return "pedido_id é obrigatório"

    valor = corpo.get("valor")
    if isinstance(valor, bool) or not isinstance(valor, (int, float)) or valor <= 0:
        return "valor deve ser um número positivo"

    webhook_url = corpo.get("webhook_url")
    if not isinstance(webhook_url, str):
        return "webhook_url é obrigatória"
    url = urlparse(webhook_url)
    if url.scheme not in ("http", "https") or not url.netloc:
        return "webhook_url deve ser uma URL http ou https"

    return None


def moeda(valor: float) -> str:
    """Formata no padrão brasileiro: R$ 4.500,00."""
    texto = f"{valor:,.2f}"
    return "R$ " + texto.replace(",", "_").replace(".", ",").replace("_", ".")


def avisar_webhook(cobranca: Cobranca, decisao: str) -> None:
    """Faz o POST do resultado no webhook do MS Pagamento. Levanta
    requests.RequestException se a loja não confirmar o recebimento com 2xx."""
    resposta = requests.post(
        cobranca.webhook_url,
        json={"cobranca_id": cobranca.id, "pedido_id": cobranca.pedido_id, "status": decisao},
        timeout=TEMPO_LIMITE_WEBHOOK_SEGUNDOS,
    )
    resposta.raise_for_status()


def criar_app(url_publica: str) -> Flask:
    """Monta o Mock. url_publica é o endereço que o navegador usa para abrir o checkout."""
    app = Flask(__name__)
    app.add_template_filter(moeda)
    cobrancas = Cobrancas()

    @app.post("/cobrancas")
    def criar_cobranca():
        corpo = request.get_json(silent=True)
        erro = validar_cobranca(corpo)
        if erro is not None:
            return jsonify(erro=erro), 400

        cobranca = cobrancas.criar(corpo["pedido_id"], float(corpo["valor"]), corpo["webhook_url"])
        registro.info(
            "cobrança %s criada para o pedido %s (%s), webhook %s",
            cobranca.id, cobranca.pedido_id, moeda(cobranca.valor), cobranca.webhook_url,
        )
        return jsonify(cobranca_id=cobranca.id, url_checkout=f"{url_publica}/checkout/{cobranca.id}"), 201

    @app.get("/checkout/<cobranca_id>")
    def checkout(cobranca_id: str):
        cobranca = cobrancas.buscar(cobranca_id)
        if cobranca is None:
            abort(404)
        return render_template("checkout.html", cobranca=cobranca)

    @app.post("/checkout/<cobranca_id>")
    def decidir(cobranca_id: str):
        cobranca = cobrancas.buscar(cobranca_id)
        if cobranca is None:
            abort(404)

        decisao = request.form.get("status")
        if decisao not in DECISOES:
            abort(400)

        if not cobrancas.iniciar_envio(cobranca.id):
            aviso = "Esta cobrança já foi decidida."
            return render_template("checkout.html", cobranca=cobranca, aviso=aviso), 409

        try:
            avisar_webhook(cobranca, decisao)
        except requests.RequestException as erro:
            cobrancas.reabrir(cobranca.id)
            registro.warning("cobrança %s: não consegui avisar a loja em %s: %s", cobranca.id, cobranca.webhook_url, erro)
            aviso = "Não consegui avisar a loja. Tente de novo em instantes."
            return render_template("checkout.html", cobranca=cobranca, aviso=aviso), 502

        cobrancas.concluir(cobranca.id, decisao)
        registro.info("cobrança %s %s; loja avisada em %s", cobranca.id, decisao, cobranca.webhook_url)
        return redirect(url_for("checkout", cobranca_id=cobranca.id), code=303)

    return app


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="[mock-pagamento] %(asctime)s %(message)s", datefmt="%H:%M:%S")
    porta = int(os.environ.get("MOCK_PORTA", "8090"))
    url_publica = os.environ.get("MOCK_URL_PUBLICA", f"http://localhost:{porta}")
    criar_app(url_publica).run(host="0.0.0.0", port=porta)


if __name__ == "__main__":
    main()
