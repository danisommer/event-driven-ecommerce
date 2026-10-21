import unittest
from unittest import mock

import requests

from app import TEMPO_LIMITE_WEBHOOK_SEGUNDOS, criar_app

WEBHOOK = "http://pagamento.teste/webhook/pagamento"


class MockDePagamentoTeste(unittest.TestCase):
    def setUp(self):
        self.cliente = criar_app("http://mock.teste").test_client()

    def criar_cobranca(self, **mudancas):
        corpo = {"pedido_id": "PED-1", "valor": 4500.0, "webhook_url": WEBHOOK, **mudancas}
        return self.cliente.post("/cobrancas", json=corpo)

    def criar_e_pegar_id(self):
        return self.criar_cobranca().get_json()["cobranca_id"]

    def decidir(self, cobranca_id, status):
        return self.cliente.post(f"/checkout/{cobranca_id}", data={"status": status})

    def test_cria_cobranca_e_devolve_url_de_checkout(self):
        resposta = self.criar_cobranca()

        self.assertEqual(resposta.status_code, 201)
        dados = resposta.get_json()
        self.assertEqual(dados["url_checkout"], f"http://mock.teste/checkout/{dados['cobranca_id']}")

    def test_recusa_cobranca_invalida(self):
        invalidas = [
            {"pedido_id": ""},
            {"valor": 0},
            {"valor": "4500"},
            {"valor": True},
            {"webhook_url": "ftp://pagamento.teste"},
            {"webhook_url": None},
        ]
        for mudanca in invalidas:
            with self.subTest(mudanca=mudanca):
                resposta = self.criar_cobranca(**mudanca)
                self.assertEqual(resposta.status_code, 400)
                self.assertIn("erro", resposta.get_json())

    def test_recusa_corpo_que_nao_e_json(self):
        resposta = self.cliente.post("/cobrancas", data="nada", content_type="text/plain")
        self.assertEqual(resposta.status_code, 400)

    def test_checkout_mostra_valor_e_os_dois_botoes(self):
        resposta = self.cliente.get(f"/checkout/{self.criar_e_pegar_id()}")

        self.assertEqual(resposta.status_code, 200)
        pagina = resposta.get_data(as_text=True)
        self.assertIn("R$ 4.500,00", pagina)
        self.assertIn("Pagamento Aprovado", pagina)
        self.assertIn("Pagamento Recusado", pagina)

    def test_checkout_inexistente_da_404(self):
        self.assertEqual(self.cliente.get("/checkout/COB-nao-existe").status_code, 404)

    @mock.patch("app.requests.post")
    def test_clique_avisa_o_webhook_da_loja(self, post):
        cobranca_id = self.criar_e_pegar_id()

        resposta = self.decidir(cobranca_id, "APROVADO")

        self.assertEqual(resposta.status_code, 303)
        post.assert_called_once_with(
            WEBHOOK,
            json={"cobranca_id": cobranca_id, "pedido_id": "PED-1", "status": "APROVADO"},
            timeout=TEMPO_LIMITE_WEBHOOK_SEGUNDOS,
        )
        pagina = self.cliente.get(f"/checkout/{cobranca_id}").get_data(as_text=True)
        self.assertNotIn("<button", pagina)
        self.assertIn("Pagamento aprovado", pagina)

    @mock.patch("app.requests.post")
    def test_segunda_decisao_da_409_e_nao_avisa_de_novo(self, post):
        cobranca_id = self.criar_e_pegar_id()
        self.decidir(cobranca_id, "APROVADO")

        resposta = self.decidir(cobranca_id, "RECUSADO")

        self.assertEqual(resposta.status_code, 409)
        post.assert_called_once()

    @mock.patch("app.requests.post")
    def test_falha_ao_avisar_a_loja_deixa_a_cobranca_pendente(self, post):
        cobranca_id = self.criar_e_pegar_id()
        post.side_effect = requests.ConnectionError("conexão recusada")

        resposta = self.decidir(cobranca_id, "APROVADO")

        self.assertEqual(resposta.status_code, 502)
        self.assertIn("Pagamento Aprovado", resposta.get_data(as_text=True))

        post.side_effect = None
        self.assertEqual(self.decidir(cobranca_id, "APROVADO").status_code, 303)

    def test_decisao_invalida_da_400(self):
        self.assertEqual(self.decidir(self.criar_e_pegar_id(), "TALVEZ").status_code, 400)


if __name__ == "__main__":
    unittest.main()
