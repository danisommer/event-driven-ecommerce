-include .env
export

.PHONY: ajuda broker broker-parar broker-logs chaves estoque pagamento entrega promocoes principal mock-instalar mock-pagamento frontend observador teste teste-mock zerar build limpar

ajuda:
	@echo "Broker"
	@echo "  make broker          sobe o RabbitMQ (painel em http://localhost:15672, guest/guest)"
	@echo "  make broker-parar    derruba o RabbitMQ"
	@echo "  make broker-logs     acompanha o log do broker"
	@echo ""
	@echo "Chaves"
	@echo "  make chaves          gera os pares RSA e distribui as chaves publicas"
	@echo ""
	@echo "Microsservicos em Go (um por terminal)"
	@echo "  make estoque         Estoque: saldo, reservas e promocoes no SQLite; GET /produtos em :8081"
	@echo "  make pagamento       Pagamento: cobranca no Mock e webhook em :8082"
	@echo "  make entrega         Entrega: nota fiscal e despacho"
	@echo "  make promocoes       Promocoes: inscricoes no SQLite, promocao no Estoque e e-mails pela Resend"
	@echo "  make principal       Principal (API Gateway): REST e SSE em :8080"
	@echo ""
	@echo "Fora do RabbitMQ (Python)"
	@echo "  make mock-instalar   cria o venv do Mock e instala as dependencias (uma vez so)"
	@echo "  make mock-pagamento  sobe o Mock de Pagamento em http://localhost:8090"
	@echo "  make frontend        serve o frontend em http://localhost:5173"
	@echo ""
	@echo "Desenvolvimento"
	@echo "  make observador      imprime todos os eventos da exchange eCommerce"
	@echo "  make teste           roda os testes em Go"
	@echo "  make teste-mock      roda os testes do Mock"
	@echo "  make zerar           apaga os bancos SQLite (estoque inicial e nenhum inscrito)"
	@echo "  make build           compila tudo em ./bin"
	@echo "  make limpar          apaga os binarios"

broker:
	docker compose up -d
	@echo "RabbitMQ subindo. Painel: http://localhost:15672 (guest / guest)"

broker-parar:
	docker compose down

broker-logs:
	docker compose logs -f rabbitmq

chaves:
	go run ./cmd/gerar-chaves

estoque:
	go run ./cmd/estoque

pagamento:
	go run ./cmd/pagamento

entrega:
	go run ./cmd/entrega

promocoes:
	go run ./cmd/promocoes

principal:
	go run ./cmd/principal

mock-instalar:
	python3 -m venv mock-pagamento/.venv
	mock-pagamento/.venv/bin/pip install -r mock-pagamento/requirements.txt

mock-pagamento:
	cd mock-pagamento && .venv/bin/python app.py

frontend:
	cd web && python3 -m http.server 5173

observador:
	go run ./cmd/observador

teste:
	go test ./...

teste-mock:
	cd mock-pagamento && .venv/bin/python -m unittest -v

zerar:
	rm -f estoque.db promocoes.db

build:
	go build -o bin/ ./cmd/...
	@ls bin/

limpar:
	rm -rf bin/
