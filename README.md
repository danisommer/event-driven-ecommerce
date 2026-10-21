# E-commerce em microsserviços

Loja virtual dividida em microsserviços. O frontend web fala por REST com um
API Gateway, que recebe as mudanças de status dos pedidos em tempo real por
SSE. Os serviços de domínio conversam só por eventos no RabbitMQ, todos
assinados com RSA. O pagamento passa por um provedor externo simulado, que
avisa o resultado por webhook, e as promoções saem por e-mail pela Resend.

Regra de linguagem: o que fala com o RabbitMQ é Go; o que não fala (Mock de
Pagamento e servidor do frontend) é Python; o frontend é HTML, CSS e
JavaScript.

## Tecnologias

| Parte | Tecnologia |
|---|---|
| Microsserviços | Go 1.26, só biblioteca padrão para HTTP: rotas do `net/http` (`GET /produtos/{id}`) e SSE com `http.ResponseController` |
| Mensageria | RabbitMQ 3.13 em Docker Compose; cliente `github.com/rabbitmq/amqp091-go` |
| Assinatura dos eventos | RSA 2048 com SHA-256 (PKCS#1 v1.5), pacote `crypto/rsa` |
| Persistência | SQLite pelo `modernc.org/sqlite` (Go puro, sem cgo), um banco por serviço |
| Tempo real | Server-Sent Events (`text/event-stream`) no Gateway e `EventSource` no navegador |
| Frontend | HTML, CSS e JavaScript sem framework (`fetch`, `EventSource`, `localStorage`), servido pelo `http.server` do Python |
| Provedor de pagamento | Python 3 com Flask 3.1 e `requests`, em venv |
| E-mail | API HTTP da Resend (`POST https://api.resend.com/emails`), chamada com `net/http` |
| Testes | `go test` com `httptest` e SQLite temporário; `unittest` com o cliente de teste do Flask |

## Arquitetura

```
Frontend (HTML/CSS/JS, :5173)
   │  REST + SSE
   ▼
Principal / API Gateway (Go, :8080) ──REST──▶ Estoque (Go, :8081, SQLite)
   │  ▲
   ▼  │   exchange eCommerce no RabbitMQ (eventos assinados com RSA)
   ├────────────┬──────────────────┬────────────┐
Estoque     Pagamento (Go, :8082)  Entrega     Promoções (Go, SQLite)
               │  ▲                               │
 cria cobrança │  │ webhook                       └──▶ API de e-mail (Resend)
               ▼  │
       Mock de Pagamento (Python, :8090) ◀── aba aberta pelo navegador
```

| Processo | Linguagem | Porta | Comando | Papel |
|---|---|---|---|---|
| Frontend | HTML/CSS/JS | 5173 | `make frontend` | catálogo, carrinho, acompanhamento dos pedidos, inscrição em promoções |
| Principal (API Gateway) | Go | 8080 | `make principal` | API REST do frontend, conexões SSE, eventos das ações do usuário |
| Estoque | Go | 8081 | `make estoque` | saldo, reservas e preços promocionais no SQLite, `GET /produtos` |
| Pagamento | Go | 8082 | `make pagamento` | cria a cobrança no Mock, recebe o webhook |
| Entrega | Go | — | `make entrega` | emite nota fiscal e despacha |
| Promoções | Go | — | `make promocoes` | guarda as inscrições no SQLite, cria as promoções e as envia por e-mail |
| Mock de Pagamento | Python (Flask) | 8090 | `make mock-pagamento` | provedor de pagamento externo: página com "Aprovado" e "Recusado" |
| RabbitMQ | — | 5672 / 15672 | `make broker` | broker; painel em http://localhost:15672 (guest / guest) |

## Estrutura do repositório

```
cmd/                um main por processo (principal, estoque, pagamento, entrega, promocoes)
                    e as ferramentas (gerar-chaves, observador, teste-publicador)
internal/
  principal/        API Gateway: rotas REST, hub SSE, registro dos pedidos, cliente do Estoque
  estoque/          regras de reserva, repositório SQLite (saldo, reservas, promoções), API HTTP
  pagamento/        cliente do provedor, estado das cobranças, rota do webhook
  entrega/          nota fiscal e despacho
  promocoes/        inscrições no SQLite, geração das promoções, envio de e-mail
  evento/           envelope, routing keys, filas e payloads: o contrato entre os serviços
  mensageria/       conexão, publicador e consumidor do RabbitMQ (assina e valida)
  cripto/           chaves RSA, assinatura e verificação
  rest/             o que as APIs HTTP têm em comum (porta, JSON, servidor junto do consumidor)
  catalogo/         catálogo fixo: carga inicial do Estoque e base das promoções
mock-pagamento/     provedor de pagamento externo (Flask)
web/                frontend
chaves/             par de chaves RSA de cada serviço
```

## Como rodar

Pré-requisitos: Go 1.26+, Docker, Python 3 com venv (no Ubuntu,
`sudo apt install python3-venv`) e `make`.

Uma vez só:

```bash
make mock-instalar                      # venv do Mock com Flask e requests
echo 'RESEND_API_KEY=re_...' > .env     # opcional: sem a chave, os e-mails só aparecem no log
```

O `.env` está no `.gitignore` e o Makefile o carrega em todo `make`.

Depois, um processo por terminal, nesta ordem:

```bash
make broker
make estoque
make mock-pagamento        # antes do Pagamento
make pagamento
make entrega
make promocoes             # na demonstração: PROMOCOES_INTERVALO=20s make promocoes
make principal
make frontend
```

Abra http://localhost:5173. Os serviços Go tentam conectar no broker por até
1 minuto, então podem subir enquanto o RabbitMQ ainda inicia. `Ctrl+C` encerra
qualquer processo e `make` sozinho lista todos os comandos.

## Fluxo de um pedido

1. O frontend lista os produtos: `GET /produtos` no Gateway, que consulta o
   Estoque por REST.
2. Ao finalizar, o frontend abre uma aba de "aguardando pagamento" e faz
   `POST /pedidos` com `cliente_id` e os itens (só produto e quantidade). O
   Gateway busca nome e preço no Estoque (já com o desconto, se houver
   promoção válida), registra o pedido, publica `pedido.criado` e responde
   `202`.
3. O Estoque reserva os itens numa transação e publica `pedido.estoque_ok`, ou
   `estoque.indisponivel` sem mexer no saldo.
4. O Pagamento cria a cobrança no Mock (`POST /cobrancas`, informando a URL do
   seu webhook) e publica `pagamento.pendente` com a `url_checkout`. O Gateway
   repassa por SSE, e o frontend leva a aba aberta no passo 2 até a página do
   Mock.
5. O comprador clica em "Pagamento Aprovado" ou "Pagamento Recusado". O servidor
   do Mock faz `POST` no webhook do Pagamento, que publica `pagamento.aprovado`
   ou `pagamento.recusado`.
6. A Entrega recebe `pagamento.aprovado` e publica `pedido.enviado`.
7. Em recusa ou falta de estoque, o Gateway publica `pedido.excluido`: o Estoque
   devolve a reserva e o Pagamento cancela a cobrança pendente.

Cada mudança chega ao navegador pelo SSE. Os status, como códigos estáveis:

| Status | Evento que leva a ele | Encerrado |
|---|---|---|
| `aguardando_estoque` | `POST /pedidos` | não |
| `estoque_confirmado` | `pedido.estoque_ok` | não |
| `aguardando_pagamento` | `pagamento.pendente` (traz `url_checkout`) | não |
| `pagamento_aprovado` | `pagamento.aprovado` | não |
| `enviado` | `pedido.enviado` | sim |
| `sem_estoque` | `estoque.indisponivel` | sim |
| `pagamento_recusado` | `pagamento.recusado` | sim |
| `cancelado` | `DELETE /pedidos/{id}` | sim |

Um pedido encerrado ignora eventos que cheguem depois.

## Eventos

Todos passam pela exchange `eCommerce`, do tipo `direct`:

| Routing key | Produtor | Consumidores |
|---|---|---|
| `pedido.criado` | Principal | Estoque |
| `pedido.excluido` | Principal | Estoque, Pagamento |
| `pedido.estoque_ok` | Estoque | Pagamento, Principal |
| `estoque.indisponivel` | Estoque | Principal |
| `pagamento.pendente` | Pagamento | Principal |
| `pagamento.aprovado` | Pagamento | Entrega, Principal |
| `pagamento.recusado` | Pagamento | Principal |
| `pedido.enviado` | Entrega | Principal |
| `interesse.promocao` | Principal | Promoções |
| `interesse.cancelado` | Principal | Promoções |
| `promocao.criada` | Promoções | Estoque |

Cada processo tem a sua fila durável: `principal.status`, `estoque.pedidos`,
`pagamento.estoque_ok`, `entrega.pagamentos` e `promocoes.interesses`.

## API do Gateway (`:8080`)

Erros sempre como `{"erro": "mensagem"}`. O CORS libera as origens de
`FRONTEND_ORIGEM`.

| Método | Caminho | Corpo | Resposta |
|---|---|---|---|
| GET | `/produtos` | — | 200 lista de produtos com saldo; 502 se o Estoque não responder |
| POST | `/pedidos` | `{cliente_id, cliente, itens: [{produto_id, quantidade}]}` | 202 pedido; 400 validação; 502 Estoque fora; 503 broker fora |
| GET | `/pedidos?cliente_id=` | — | 200 pedidos do cliente |
| DELETE | `/pedidos/{id}?cliente_id=` | — | 202 pedido cancelado; 404 de outro cliente ou inexistente; 409 já encerrado |
| GET | `/eventos?cliente_id=` | — | 200 `text/event-stream` |
| POST | `/interesses` | `{email, categorias: ["A", "C"]}` | 202; 400 e-mail ou categoria inválidos |
| DELETE | `/interesses/{email}` | — | 202 |

O SSE manda o pedido inteiro a cada mudança, e um comentário a cada 15 s para
a conexão não cair por inatividade:

```
event: pedido
data: {"id":"PED-942622","status":"aguardando_pagamento","url_checkout":"http://localhost:8090/checkout/COB-da52a7e0", ...}

: ping
```

O `cliente_id` é gerado pelo frontend na primeira visita e guardado no
navegador; é ele que liga o SSE aos pedidos do cliente. Duas abas do mesmo
navegador recebem as mesmas mudanças.

## APIs entre serviços

| Serviço | Método e caminho | Uso |
|---|---|---|
| Estoque | `GET /produtos` | produtos com `quantidade > 0`; `preco` é o que vale agora e `preco_original` aparece quando há promoção |
| Estoque | `GET /produtos/{id}` | produto com qualquer saldo (o Gateway busca o preço aqui); 404 se não existe |
| Mock | `POST /cobrancas` `{pedido_id, valor, webhook_url}` | 201 `{cobranca_id, url_checkout}` |
| Mock | `GET /checkout/{id}` | página com os dois botões |
| Mock | `POST /checkout/{id}` | o servidor do Mock chama o webhook; 409 se já decidida |
| Pagamento | `POST /webhook/pagamento` `{cobranca_id, pedido_id, status}` | 200 (também para aviso repetido); 400 status inválido; 404 cobrança desconhecida |

## Promoções por e-mail

O frontend inscreve um e-mail em uma ou mais categorias. Inscrever de novo troca
as categorias; cancelar remove todas. O Promoções guarda as inscrições no
SQLite e, a cada `PROMOCOES_INTERVALO` (padrão `2m`), sorteia um produto entre
as categorias que têm inscritos e um desconto de 5% a 44%, válido por 1 a 9
horas. Então:

1. Publica `promocao.criada`. O Estoque, dono do preço, guarda o preço
   promocional com a validade; até lá, as consultas de produto (e com elas os
   pedidos montados pelo Gateway) usam o preço com desconto. O desconto vale
   para qualquer cliente; o e-mail só avisa os interessados.
2. Envia um e-mail a cada inscrito da categoria pela API da Resend
   (`POST https://api.resend.com/emails`).

Se a publicação falhar, nenhum e-mail sai: a loja não anuncia um preço que não
vai cobrar. Uma promoção nova para o mesmo produto substitui a anterior.

Com o remetente de testes `onboarding@resend.dev`, a Resend só entrega para o
e-mail dono da conta; os outros endereços recebem `403`, que aparece no log.
Sem `RESEND_API_KEY`, os e-mails só são registrados no log.

## Assinatura digital

Todo evento carrega no envelope o campo `signature`: o SHA-256 do evento (sem
esse campo) assinado com a chave privada RSA de quem publicou. Quem consome
confere com a chave pública do `produtor` declarado e descarta o evento se não
bater.

Para ver um evento falso sendo descartado, publique pelo painel do RabbitMQ
(*Exchanges* → `eCommerce` → *Publish message*), com a routing key
`pedido.criado` e o payload:

```json
{"id":"falso","tipo":"pedido.criado","produtor":"principal","timestamp":"agora","dados":{"pedido_id":"PED-FALSO","itens":[]},"signature":"AAAA"}
```

O log do Estoque mostra `assinatura inválida no evento pedido.criado vindo de
"principal", DESCARTADO`. Com `VERIFICAR_ASSINATURA=off make estoque`, a mesma
mensagem é aceita e recusada por outro motivo (`pedido sem itens`).

## Persistência

| Dado | Onde |
|---|---|
| Produtos, saldo, reservas e preços promocionais | `estoque.db` (SQLite), criado com o catálogo abaixo na primeira execução |
| Inscrições em promoções | `promocoes.db` (SQLite) |
| Pedidos do Gateway, cobranças do Pagamento e do Mock | memória: reiniciar o processo apaga |

`make zerar` apaga os dois bancos: o Estoque volta às quantidades e aos preços
iniciais e ninguém fica inscrito.

| Código | Produto | Categoria | Preço | Estoque inicial |
|---|---|---|---|---|
| P001 | Notebook Gamer 16GB | A | R$ 4.500,00 | 10 |
| P002 | Mouse sem fio | A | R$ 150,00 | 50 |
| P003 | Teclado mecânico | A | R$ 320,00 | 25 |
| P004 | Cafeteira expresso | B | R$ 280,00 | 15 |
| P005 | Liquidificador 900W | B | R$ 190,00 | 20 |
| P006 | Air Fryer 5L | B | R$ 450,00 | 8 |
| P007 | Camiseta algodão | C | R$ 79,90 | 100 |
| P008 | Tênis de corrida | C | R$ 350,00 | 30 |
| P009 | Mochila 30L | C | R$ 210,00 | 12 |

## Decisões de projeto

- **Eventos entre serviços, REST só nas bordas.** O REST aparece entre
  frontend e Gateway, entre Gateway e Estoque (consulta de produtos) e entre
  Pagamento e Mock. Todo o resto passa pelo RabbitMQ.
- **`POST /pedidos` responde `202 Accepted`.** O pedido é aceito e processado
  de forma assíncrona; o resultado chega pelo SSE.
- **O preço vem do Estoque.** O frontend manda só produto e quantidade; o
  Gateway busca nome e preço (já com promoção) no Estoque.
- **Quem decide se há estoque é o Estoque**, numa transação que confere o saldo
  e baixa a reserva de uma vez. O Gateway não confere saldo.
- **A URL do pagamento chega por evento** (`pagamento.pendente`), porque ela só
  existe depois que o estoque confirma, quando a resposta do `POST` já foi.
- **Status como códigos estáveis** (`aguardando_pagamento`, …): o frontend
  decide por eles e traduz para o texto que mostra.
- **Idempotência:** reentrega de evento ou webhook repetido não reserva, não
  cobra e não publica duas vezes.
- **Pedido encerrado ignora eventos atrasados**, e o Pagamento cancela a
  cobrança de um pedido excluído, para um pedido cancelado não ser despachado.
- **O SSE nunca trava o consumidor do RabbitMQ:** cada conexão tem uma fila
  curta; a que não acompanha é derrubada, e o navegador reconecta e busca o
  estado atual.
- **A promoção só é anunciada depois de registrada no Estoque**, para o e-mail
  nunca prometer um preço que a loja não cobra.

## Limitações conhecidas

- Pedidos (Gateway) e cobranças (Pagamento e Mock) ficam em memória: reiniciar
  o processo apaga.
- Não há login: o `cliente_id` gerado no navegador identifica o cliente.
- O webhook do Mock não é autenticado; provedores reais assinam o webhook (por
  exemplo, com HMAC).
- Preços em ponto flutuante, como no Trab1; o certo seria centavos inteiros.
- No modo de testes da Resend, só o e-mail dono da conta recebe.
- O catálogo do frontend mostra uma promoção nova ao recarregar a página ou no
  próximo evento de pedido.

## Roteiro de demonstração

Com tudo no ar (de preferência depois de `make zerar`):

1. **Catálogo vindo do Estoque:** abra o frontend; o log do Estoque mostra o
   `GET /produtos` feito pelo Gateway.
2. **Pedido completo:** adicione um produto e finalize. A nova aba vai para o
   Mock; clique em "Pagamento Aprovado". Em "Meus pedidos" o status passa por
   Estoque confirmado → Aguardando pagamento → Pagamento aprovado → Pedido
   enviado, sem recarregar a página.
3. **Pagamento recusado:** repita e clique em "Pagamento Recusado". O pedido
   fica recusado e o log do Estoque mostra os itens devolvidos.
4. **Sem estoque:** peça 9 Air Fryers (`P006` começa com 8).
5. **Cancelamento:** finalize um pedido, feche a aba do Mock sem pagar e use
   "Cancelar pedido". O Estoque devolve a reserva e o Pagamento cancela a
   cobrança.
6. **Persistência:** reinicie o Estoque; o saldo continua o mesmo.
7. **Duas abas:** abra o frontend em duas abas e faça um pedido; as duas
   atualizam.
8. **Promoções:** inscreva o e-mail da conta da Resend com
   `PROMOCOES_INTERVALO=20s`; o e-mail chega e o log do Estoque mostra a
   promoção registrada. Recarregue o frontend: o produto aparece com o preço
   riscado e o selo de desconto, e um pedido dele sai com o preço promocional.
   Cancele a inscrição e os envios param.
9. **Assinatura:** publique o evento falso da seção acima.

`make observador` mostra todos os eventos passando pela exchange num terminal
só.

## Testes

```bash
make teste                              # Go, sem precisar do broker
make teste-mock                         # Mock de Pagamento (unittest)
go test -race ./internal/principal/     # concorrência do Gateway (precisa de gcc)
```

Os serviços recebem publicador, provedor, Estoque e notificador por interface,
e os testes injetam versões falsas. Os bancos de teste são SQLite temporários.

## Variáveis de ambiente

| Variável | Padrão | Quem usa |
|---|---|---|
| `RABBITMQ_URL` | `amqp://guest:guest@localhost:5672/` | todos os serviços Go |
| `CHAVES_DIR` | `chaves` | todos os serviços Go |
| `VERIFICAR_ASSINATURA` | ligada | consumidores; `off` aceita eventos sem validar |
| `GATEWAY_PORTA` | `8080` | Principal |
| `ESTOQUE_URL` | `http://localhost:8081` | Principal |
| `FRONTEND_ORIGEM` | `http://localhost:5173,http://127.0.0.1:5173` | Principal (CORS) |
| `ESTOQUE_PORTA` | `8081` | Estoque |
| `ESTOQUE_BANCO` | `estoque.db` | Estoque |
| `PAGAMENTO_PORTA` | `8082` | Pagamento |
| `PAGAMENTO_WEBHOOK_URL` | `http://localhost:8082/webhook/pagamento` | Pagamento |
| `MOCK_PAGAMENTO_URL` | `http://localhost:8090` | Pagamento |
| `MOCK_PORTA` | `8090` | Mock |
| `MOCK_URL_PUBLICA` | `http://localhost:8090` | Mock (monta a `url_checkout`) |
| `RESEND_API_KEY` | — | Promoções; sem ela, os e-mails só vão para o log |
| `EMAIL_REMETENTE` | `Loja SD <onboarding@resend.dev>` | Promoções |
| `PROMOCOES_BANCO` | `promocoes.db` | Promoções |
| `PROMOCOES_INTERVALO` | `2m` | Promoções |

## Ferramentas de desenvolvimento

**`make observador`** imprime todo evento que passa pela exchange `eCommerce`.

**`teste-publicador`** publica `pedido.criado` ou `pedido.excluido` assinado,
sem passar pelo Gateway:

```bash
go run ./cmd/teste-publicador -itens P001:2,P004:1   # pedido que cabe no estoque
go run ./cmd/teste-publicador -itens P006:99         # pedido sem estoque
go run ./cmd/teste-publicador -tipo pedido.excluido -pedido PED-001 -motivo teste
```

| Flag | Padrão | Para que serve |
|---|---|---|
| `-tipo` | `pedido.criado` | `pedido.criado` ou `pedido.excluido` |
| `-pedido` | gerado pelo horário | ID do pedido |
| `-itens` | `P001:1` | itens no formato `PRODUTO:QTD,PRODUTO:QTD` |
| `-cliente` | `cliente-teste` | nome do cliente |
| `-motivo` | `cancelado no teste` | motivo do `pedido.excluido` |
| `-como` | `principal` | serviço cuja chave assina o evento |

Pedidos publicados assim não aparecem no frontend, porque o Gateway não os
conhece; o resto do fluxo (Estoque, Pagamento, Mock, Entrega) funciona igual.

## Chaves

```
chaves/
  <serviço>/
    privada.pem   assina o que o serviço publica
    publicas/     chaves públicas de todos, para validar o que chega
```

As chaves estão versionadas para todos usarem o mesmo conjunto. `make chaves`
preserva as existentes; `go run ./cmd/gerar-chaves -forcar` regera tudo.
