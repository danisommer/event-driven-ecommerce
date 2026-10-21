Situação: fases 0 a 8 implementadas. O que falta é validação, marcada com `[ ]`.

## Decisões

- [X] Stack do frontend (linguagem diferente de Go): HTML/CSS/JS e Python para eventuais servidores/backend
- [X] Persistência do Estoque e das inscrições: SQLite
- [X] E-mail: conta na Resend criada; a chave fica no `.env` da raiz (fora do git) e o Makefile a carrega
- [X] Integração com a Resend feita com `net/http` (`POST https://api.resend.com/emails`), sem o SDK `resend-go`
- [X] Remover o menu de terminal e os consumidores C1/C2: Sim
- [X] Regra de linguagem: o que fala com o RabbitMQ é Go; o que não fala (Mock de Pagamento, servidor do frontend) é Python
- [X] Mock de Pagamento em Python, no mesmo repo, sem acesso ao RabbitMQ nem às chaves
- [X] Eventos novos: `interesse.promocao`, `interesse.cancelado`, `pagamento.pendente`
- [X] Status do pedido expostos como código (tabela 0.4)
- [X] Promoção vale no pedido: o Promoções publica `promocao.criada` e o Estoque, dono do preço, aplica o desconto (fase 8)

| Código | Evento que leva a ele | Pedido no enunciado | Encerrado |
|---|---|---|---|
| `aguardando_estoque` | `POST /pedidos` | — | não |
| `estoque_confirmado` | `pedido.estoque_ok` | Estoque Confirmado | não |
| `aguardando_pagamento` | `pagamento.pendente` (traz `url_checkout`) | — | não |
| `pagamento_aprovado` | `pagamento.aprovado` | Pagamento Aprovado | não |
| `pagamento_recusado` | `pagamento.recusado` | Pagamento Recusado | sim |
| `sem_estoque` | `estoque.indisponivel` | — | sim |
| `enviado` | `pedido.enviado` | Pedido Enviado | sim |
| `cancelado` | `DELETE /pedidos/{id}` | — | sim |

---

## Visão geral

| Processo | Porta | Fala HTTP com | RabbitMQ |
|---|---|---|---|
| Frontend (HTML/CSS/JS, servido por Python) | 5173 | Gateway | — |
| Gateway (Principal) | 8080 | Estoque | publica `pedido.*` e `interesse.*`; consome os status |
| Estoque | 8081 | — | consome `pedido.criado`, `pedido.excluido` e `promocao.criada` |
| Pagamento | 8082 | Mock | consome `pedido.estoque_ok` e `pedido.excluido`; publica `pagamento.*` |
| Entrega | — | — | consome `pagamento.aprovado`; publica `pedido.enviado` |
| Promoções | — | Resend | consome `interesse.*`; publica `promocao.criada` |
| Mock de Pagamento (Python) | 8090 | Pagamento (webhook) | — (sistema externo) |
| RabbitMQ | 5672 / 15672 | — | — |

Fluxo do pedido:

1. Frontend `GET /produtos` → Gateway → Estoque `GET /produtos`.
2. Frontend `POST /pedidos` → Gateway busca o preço no Estoque, registra o pedido e publica `pedido.criado`; responde `202`.
3. Estoque reserva e publica `pedido.estoque_ok` → Gateway → SSE.
4. Pagamento cria a cobrança no Mock (`POST /cobrancas` com `webhook_url`) e publica `pagamento.pendente` com a `url_checkout` → Gateway → SSE → frontend mostra o botão de pagar.
5. Usuário clica no Mock → Mock `POST` no webhook do Pagamento → `pagamento.aprovado` ou `pagamento.recusado` → Gateway → SSE.
6. Entrega publica `pedido.enviado` → Gateway → SSE.

Fluxo de promoções:

1. Frontend `POST /interesses` → Gateway publica `interesse.promocao` → Promoções grava a inscrição.
2. O gerador de promoções sorteia uma promoção entre as categorias com inscritos e publica `promocao.criada`; o Estoque guarda o preço promocional até a validade.
3. Só então o Promoções envia um e-mail a cada inscrito da categoria pela Resend. Os pedidos seguintes do produto já saem com o desconto.

---

## Fase 0 — Contratos

Objetivo: fixar tudo que cruza a fronteira entre processos antes de escrever os serviços. Com isso pronto, as fases 1, 2, 3, 5 e 6 podem andar em paralelo.

### 0.1 Eventos (exchange `eCommerce`, direct)

| Routing key | Produtor | Consumidores | Payload | Situação |
|---|---|---|---|---|
| `pedido.criado` | Gateway | Estoque | `DadosPedidoCriado` | existe |
| `pedido.excluido` | Gateway | Estoque (+ Pagamento, *extra*) | `DadosPedidoExcluido` | existe |
| `pedido.estoque_ok` | Estoque | Pagamento, Gateway | `DadosPedidoEstoqueOK` | existe |
| `estoque.indisponivel` | Estoque | Gateway | `DadosEstoqueIndisponivel` | existe |
| `pagamento.pendente` | Pagamento | Gateway | `DadosPagamentoPendente` | **novo** |
| `pagamento.aprovado` | Pagamento | Entrega, Gateway | `DadosPagamentoAprovado` | existe |
| `pagamento.recusado` | Pagamento | Gateway | `DadosPagamentoRecusado` | existe |
| `pedido.enviado` | Entrega | Gateway | `DadosPedidoEnviado` | existe |
| `interesse.promocao` | Gateway | Promoções | `DadosInteressePromocao` | **novo** |
| `interesse.cancelado` | Gateway | Promoções | `DadosInteresseCancelado` | **novo** |
| `promocao.criada` | Promoções | Estoque | `DadosPromocaoCriada` | **novo** (fase 8) |

Payloads novos:

```
DadosPagamentoPendente   { pedido_id, cobranca_id, url_checkout, valor }
DadosInteressePromocao   { email, categorias: ["A", "B"] }
DadosInteresseCancelado  { email }
DadosPromocaoCriada      { produto_id, preco_promocional, desconto_percentual, valida_ate }
```

Semântica:

- `interesse.promocao` **substitui** as categorias daquele e-mail (inscrever de novo com outras categorias troca, não soma).
- `interesse.cancelado` de um e-mail que não está inscrito é ignorado sem erro.

Filas:

- `principal.status` ganha o binding `pagamento.pendente`.
- Fila nova `promocoes.interesses` com os bindings `interesse.promocao` e `interesse.cancelado`.

Tarefas:

- [x] `internal/evento/envelope.go`: constantes `PagamentoPendente`, `InteressePromocao`, `InteresseCancelado`
- [x] `internal/evento/payloads.go`: structs `DadosPagamentoPendente`, `DadosInteressePromocao`, `DadosInteresseCancelado`
- [x] `internal/evento/filas.go`: constante `FilaPromocoes`
- [x] `internal/principal/pedidos.go`: status viram os códigos da tabela 0.4 (`StatusEstoqueReservado` → `StatusEstoqueConfirmado`, `StatusCanceladoPeloUso` → `StatusCancelado`, novo `StatusAguardandoPagamento`)

### 0.2 REST do Gateway (`:8080`)

Erros sempre no formato `{"erro": "mensagem"}`.

| Método | Caminho | Corpo | Sucesso | Erros | Nota |
|---|---|---|---|---|---|
| GET | `/produtos` | — | 200 `[Produto]` | 502 Estoque fora | (0,1) |
| POST | `/pedidos` | `{cliente_id, cliente, itens: [{produto_id, quantidade}]}` | 202 `Pedido` | 400 validação, 502 Estoque fora, 503 broker fora | (0,1) |
| GET | `/pedidos?cliente_id=` | — | 200 `[Pedido]` | 400 | *extra* (recarregar a página) |
| DELETE | `/pedidos/{id}?cliente_id=` | — | 202 `Pedido` | 404, 409 já encerrado | *extra* (o Trab1 já tinha) |
| GET | `/eventos?cliente_id=` | — | 200 `text/event-stream` | 400 | (0,5) |
| POST | `/interesses` | `{email, categorias: ["A", "B"]}` | 202 | 400 | (0,1) |
| DELETE | `/interesses/{email}` | — | 202 | 400 | (0,1) |

Formatos:

```
Produto { id, nome, categoria, preco, quantidade }

Pedido  { id, cliente_id, cliente,
          itens: [{ produto_id, nome, quantidade, preco_unitario }],
          total, status, detalhe, url_checkout, criado_em,
          historico: [{ quando, status, detalhe }] }
```

Regras:

- O frontend manda só `produto_id` e `quantidade`. Nome e preço vêm do Estoque.
- O Gateway **não** confere a quantidade disponível: quem decide é o Estoque, pelo evento.
- O Gateway **não** guarda inscrições: só valida o formato e publica o evento. Por isso o `DELETE /interesses` responde 202 mesmo para e-mail não inscrito.

### 0.3 SSE

- `GET /eventos?cliente_id=X` com `Content-Type: text/event-stream`.
- A cada mudança de um pedido desse cliente:

  ```
  event: pedido
  data: {Pedido completo, em JSON}

  ```

- A cada 15 s, um comentário `: ping` para manter a conexão viva.
- Manda o pedido inteiro, não só o que mudou: o frontend substitui pelo `id`, e perder uma mensagem intermediária não deixa a tela inconsistente.
- Ao conectar e ao reconectar, o frontend chama `GET /pedidos?cliente_id=` para recuperar o que aconteceu enquanto estava desconectado.

### 0.4 Status do pedido

| Código | Evento que leva a ele | Pedido no enunciado | Encerrado |
|---|---|---|---|
| `aguardando_estoque` | `POST /pedidos` | — | não |
| `estoque_confirmado` | `pedido.estoque_ok` | Estoque Confirmado | não |
| `aguardando_pagamento` | `pagamento.pendente` (traz `url_checkout`) | — | não |
| `pagamento_aprovado` | `pagamento.aprovado` | Pagamento Aprovado | não |
| `pagamento_recusado` | `pagamento.recusado` | Pagamento Recusado | sim |
| `sem_estoque` | `estoque.indisponivel` | — | sim |
| `enviado` | `pedido.enviado` | Pedido Enviado | sim |
| `cancelado` | `DELETE /pedidos/{id}` | — | sim |

### 0.5 REST entre serviços

Estoque (`:8081`):

| Método | Caminho | Resposta |
|---|---|---|
| GET | `/produtos` | 200 `[Produto]`, só os com `quantidade > 0`; `preco` já com promoção válida e `preco_original` quando houver |
| GET | `/produtos/{id}` | 200 `Produto` com qualquer quantidade (mesmas regras de preço), 404 se não existe |

Mock de Pagamento (`:8090`):

| Método | Caminho | Corpo | Resposta |
|---|---|---|---|
| POST | `/cobrancas` | `{pedido_id, valor, webhook_url}` | 201 `{cobranca_id, url_checkout}`, 400 |
| GET | `/checkout/{cobranca_id}` | — | 200 página HTML com os dois botões, 404 |
| POST | `/checkout/{cobranca_id}` | formulário `status=APROVADO` ou `RECUSADO` | o Mock faz `POST webhook_url` e devolve uma página de resultado; 409 se já decidida |

Webhook enviado pelo Mock ao Pagamento: `{cobranca_id, pedido_id, status}`.

Pagamento (`:8082`):

| Método | Caminho | Corpo | Resposta |
|---|---|---|---|
| POST | `/webhook/pagamento` | `{cobranca_id, pedido_id, status}` | 200 (inclusive aviso repetido), 400 status inválido, 404 cobrança desconhecida |

### 0.6 Variáveis de ambiente

| Variável | Padrão | Quem usa |
|---|---|---|
| `GATEWAY_PORTA` | `8080` | Gateway |
| `ESTOQUE_URL` | `http://localhost:8081` | Gateway |
| `FRONTEND_ORIGEM` | `http://localhost:5173` | Gateway (CORS) |
| `ESTOQUE_PORTA` | `8081` | Estoque |
| `ESTOQUE_BANCO` | `estoque.db` | Estoque |
| `PAGAMENTO_PORTA` | `8082` | Pagamento |
| `PAGAMENTO_WEBHOOK_URL` | `http://localhost:8082/webhook/pagamento` | Pagamento |
| `MOCK_PAGAMENTO_URL` | `http://localhost:8090` | Pagamento |
| `MOCK_PORTA` | `8090` | Mock |
| `MOCK_URL_PUBLICA` | `http://localhost:8090` | Mock (monta a `url_checkout`) |
| `RESEND_API_KEY` | — (sem ela: modo log); fica no `.env` | Promoções |
| `EMAIL_REMETENTE` | `Loja SD <onboarding@resend.dev>` | Promoções |
| `PROMOCOES_BANCO` | `promocoes.db` | Promoções |
| `PROMOCOES_INTERVALO` | `2m` | Promoções |

As que já existem (`RABBITMQ_URL`, `CHAVES_DIR`, `VERIFICAR_ASSINATURA`) continuam. `TAXA_APROVACAO` sai na fase 3.

O Makefile carrega o `.env` da raiz (`-include .env` + `export`), então qualquer variável escrita lá chega aos processos subidos com `make`. O `.env` está no `.gitignore`.

### Pronto quando

- [x] Contratos acima revisados e aceitos
- [x] `go build ./...` e `go test ./...` passando

---

## Fase 1 — Estoque (0,2)

- [x] Persistência (0,1): tabelas `produtos` e `reservas`; carga inicial a partir do `catalogo` só quando o banco está vazio
- [x] Reserva e devolução dentro de uma transação (confere o saldo, baixa e grava a reserva de uma vez)
- [x] `Servico` passa a usar o banco no lugar dos maps, com as mesmas regras e os mesmos eventos
- [x] HTTP (0,1): `GET /produtos` e `GET /produtos/{id}`
- [x] `cmd/estoque/main.go`: sobe o HTTP e o consumidor juntos
- [x] Pacote `internal/rest` (porta, resposta JSON, HTTP + consumidor), reaproveitado nas fases 3 e 4
- [x] Ajustar `servico_test.go` (banco temporário por teste) + teste de persistência + testes do HTTP
- [x] `.gitignore`: `*.db`
- [x] Validado por vocês: `go mod tidy` e `make teste`
- [ ] Conferência manual com o broker e o Estoque no ar (comandos `curl` na seção "Pronto quando" abaixo) e reinício do Estoque

Pronto quando: `curl localhost:8081/produtos` lista os produtos e reiniciar o Estoque mantém o saldo.

## Fase 2 — Mock de Pagamento (0,2)

- [x] Processo novo em Python (Flask) na pasta `mock-pagamento/`, com venv e `requirements.txt`
- [x] `POST /cobrancas` (0,1)
- [x] `GET /checkout/{id}`: página com pedido, valor e os botões "Pagamento Aprovado" e "Pagamento Recusado" (0,1)
- [x] `POST /checkout/{id}`: o servidor do Mock chama o webhook (servidor para servidor)
- [x] Cobrança já decidida não decide de novo; falha no webhook devolve a cobrança para pendente
- [x] Testes com o cliente de teste do Flask (`unittest`, sem dependência extra)
- [x] Makefile: `mock-instalar`, `mock-pagamento`, `teste-mock`
- [ ] Validado por vocês: `make mock-instalar`, `make teste-mock` e a página aberta no navegador

Pronto quando: `curl -X POST localhost:8090/cobrancas ...` devolve a URL, a página abre e o clique dispara o POST no webhook.

## Fase 3 — Pagamento (0,2)

- [x] Remover o sorteio (`TAXA_APROVACAO`) e os testes `TestTaxa*`
- [x] Ao consumir `pedido.estoque_ok`: criar a cobrança no Mock com a `webhook_url` (0,1), guardar a cobrança (pedido, valor, itens) e publicar `pagamento.pendente`
- [x] Mock fora do ar, timeout ou recusa: publicar `pagamento.recusado` com o motivo "não foi possível criar a cobrança no provedor de pagamento"
- [x] `POST /webhook/pagamento` (0,1): `APROVADO` publica `pagamento.aprovado` (com os itens, que a Entrega usa); `RECUSADO` publica `pagamento.recusado`
- [x] Webhook idempotente: o segundo aviso da mesma cobrança é ignorado; falha ao publicar mantém a cobrança pendente
- [x] *extra*: consumir `pedido.excluido` e cancelar a cobrança pendente (ou impedir que ela seja criada)
- [x] `cmd/pagamento/main.go`: sobe o HTTP e o consumidor juntos
- [x] Testes: provedor e publicador falsos; webhook com `httptest.NewRecorder`; cliente do Mock com `httptest.Server`
- [ ] Validado por vocês: `make teste` e o fluxo Estoque → Pagamento → Mock → webhook

Pronto quando: um pedido publicado pelo `teste-publicador` gera a cobrança, e clicar no Mock publica o evento certo.

## Fase 4 — Gateway (1,0)

- [x] `Pedido` ganha `ClienteID` e `URLCheckout`
- [x] `Servico.Tratar` trata `pagamento.pendente`
- [x] Pedido encerrado ignora eventos que chegarem depois (ex.: `pagamento.pendente` de um pedido já cancelado não pode reabrir o status)
- [x] O callback `avisar func(string)` passa a receber o `Pedido`, para alimentar o SSE
- [x] Cliente HTTP do Estoque
- [x] Hub SSE (0,5): conexões por `cliente_id`, buffer por conexão, heartbeat, conexão lenta derrubada
- [x] Handlers REST da tabela 0.2 (0,1 cada) + CORS
- [x] Publicar `interesse.promocao` e `interesse.cancelado` (0,1)
- [x] `cmd/principal/main.go`: troca o menu por `http.Server` (sem `WriteTimeout`); log volta para o terminal
- [x] `rest.Rodar` cancela as requisições no Ctrl+C (senão o SSE segura o desligamento); `rest.Ambiente` no lugar das cópias locais
- [x] Remover `menu.go`, `menu_test.go` e o teste de menu em `concorrencia_test.go`
- [x] Observador escuta também `pagamento.pendente` e `interesse.*`
- [x] Testes: serviço, handlers com `httptest`, hub SSE e um stream SSE de verdade
- [ ] Validado por vocês: `make teste`, `go test -race ./internal/principal/` e o `curl -N` abaixo

Pronto quando: com `curl -N localhost:8080/eventos?cliente_id=x` aberto, um `POST /pedidos` faz os status aparecerem no stream.

## Fase 5 — Promoções (0,4)

- [x] Consumir `promocoes.interesses` (0,1): grava (substituindo as categorias) ou remove a inscrição
- [x] Persistir as inscrições (SQLite, `promocoes.db`)
- [x] Interface `Notificador` com implementação Resend via `net/http`, sem SDK (0,3); sem chave, só registra no log
- [x] Gerador: sorteia o produto entre as categorias com inscritos e envia um e-mail por inscrito da categoria (0,3); falha num e-mail não impede os outros
- [x] Intervalo configurável (`PROMOCOES_INTERVALO`); o gatilho manual ficou de fora (na demonstração, usar `PROMOCOES_INTERVALO=20s`)
- [x] Cópia local da lista de produtos removida: usa o `catalogo`
- [ ] *extra*: lista de produtos vem do Estoque (promover só o que tem saldo)
- [x] `cmd/promocoes/main.go`: consumidor e gerador em paralelo; não publica mais na exchange `promocoes`
- [x] Testes com notificador falso, banco temporário e Resend falsa (`httptest`)
- [ ] Validado por vocês: `make teste` e um e-mail de promoção chegando

Pronto quando: inscrever o próprio e-mail numa categoria e receber a promoção.

## Fase 6 — Frontend (0,5)

- [x] Estrutura em `web/` (`index.html`, `estilo.css`, `app.js`), servida por `make frontend` (`python3 -m http.server 5173`)
- [x] Catálogo com filtro por categoria e carrinho (`GET /produtos`, `POST /pedidos`); o carrinho aceita mais que o saldo mostrado, porque quem decide é o Estoque
- [x] `cliente_id` gerado no primeiro acesso e guardado no `localStorage`
- [x] Meus pedidos com status ao vivo (`EventSource` em `/eventos`; `GET /pedidos` ao abrir a conexão), linha do tempo e histórico
- [x] Nova aba aberta no clique de "Finalizar pedido" e levada até a `url_checkout` quando ela chega; botão "Ir para o pagamento" como reserva
- [x] Aviso visual a cada mudança de status e indicador da conexão em tempo real
- [x] Cancelar pedido (`DELETE /pedidos/{id}`)
- [x] Promoções: e-mail + categorias A, B e C; inscrever e cancelar
- [x] Mensagens de erro (Gateway fora do ar, validação)
- [ ] Validado por vocês: roteiro da defesa pela interface

Pronto quando: o roteiro da defesa abaixo roda inteiro pela interface.

## Fase 7 — Limpeza e documentação

- [x] Remover C1/C2, `internal/consumidor`, a exchange `promocoes`, `chaves/consumidores`, as constantes de fila e binding deles, `evento.DadosPromocao`, `mensageria.PublicarPromocao` e `cripto.CarregarChaveiroPublico`
- [x] Remover `mensageria.DefinirLog` e `principal.log` do `.gitignore` (sobras do menu de terminal)
- [x] Makefile: alvos `frontend` e `zerar`, sem `c1`/`c2`, `ajuda` atualizada
- [x] README reescrito: arquitetura, como rodar, fluxo, eventos, APIs, promoções, persistência, roteiro de demonstração, variáveis
- [ ] Apagar a exchange `promocoes` e as filas `promocoes.c1`/`promocoes.c2` que ficaram no broker (painel ou `docker compose down -v`)

## Fase 8 — Promoção vale no pedido (*extra*)

Achado no teste de ponta a ponta: o e-mail anunciava R$ 167,20 e o pedido saiu por R$ 190,00, porque a promoção só existia no Promoções e o dono do preço é o Estoque.

- [x] Evento `promocao.criada` (Promoções → Estoque) com `produto_id`, `preco_promocional`, `desconto_percentual` e `valida_ate`
- [x] Promoções publica o evento antes dos e-mails; se a publicação falhar, nenhum e-mail sai; preço promocional arredondado para centavos
- [x] Estoque guarda a promoção (tabela `promocoes`) e devolve o preço efetivo, com `preco_original` quando há promoção válida
- [x] Gateway sem mudança de regra: já usava o preço do Estoque; só repassa `preco_original`
- [x] Frontend mostra o preço riscado e o selo de desconto
- [x] Testes nos três serviços; README e observador atualizados
- [ ] Validado por vocês: `make teste` e o roteiro 8 do README (e-mail → produto com desconto no catálogo → pedido com o preço promocional)

