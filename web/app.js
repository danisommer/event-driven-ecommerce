"use strict";

const API = `${location.protocol}//${location.hostname}:8080`;

const CATEGORIAS = [
  { codigo: "A", nome: "Informática" },
  { codigo: "B", nome: "Casa e cozinha" },
  { codigo: "C", nome: "Moda e esporte" },
];

const ROTULOS_STATUS = {
  aguardando_estoque: "Aguardando estoque",
  estoque_confirmado: "Estoque confirmado",
  aguardando_pagamento: "Aguardando pagamento",
  pagamento_aprovado: "Pagamento aprovado",
  enviado: "Pedido enviado",
  sem_estoque: "Sem estoque",
  pagamento_recusado: "Pagamento recusado",
  cancelado: "Cancelado",
};

const TOM_STATUS = {
  aguardando_estoque: "andamento",
  estoque_confirmado: "andamento",
  aguardando_pagamento: "atencao",
  pagamento_aprovado: "sucesso",
  enviado: "sucesso",
  sem_estoque: "erro",
  pagamento_recusado: "erro",
  cancelado: "erro",
};

const ENCERRADOS = new Set(["enviado", "sem_estoque", "pagamento_recusado", "cancelado"]);

const moeda = new Intl.NumberFormat("pt-BR", { style: "currency", currency: "BRL" });
const horario = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "medium" });

const estado = {
  clienteId: lerOuCriarClienteId(),
  produtos: [],
  filtro: "",
  carrinho: new Map(),
  enviando: false,
  pedidos: new Map(),
  historicosAbertos: new Set(),
  abasDePagamento: new Map(),
};

const $ = (id) => document.getElementById(id);

/** Cria um elemento com atributos (on* viram ouvintes de evento) e filhos. */
function el(tag, atributos = {}, ...filhos) {
  const elemento = document.createElement(tag);
  for (const [nome, valor] of Object.entries(atributos)) {
    if (nome.startsWith("on")) {
      elemento.addEventListener(nome.slice(2), valor);
    } else if (valor === true) {
      elemento.setAttribute(nome, "");
    } else if (valor !== false && valor != null) {
      elemento.setAttribute(nome, valor);
    }
  }
  elemento.append(...filhos.filter((filho) => filho != null && filho !== false));
  return elemento;
}

function lerArmazenado(chave) {
  try {
    return localStorage.getItem(chave);
  } catch {
    return null;
  }
}

function guardar(chave, valor) {
  try {
    localStorage.setItem(chave, valor);
  } catch {}
}

/** Devolve o identificador deste navegador, criando um na primeira visita. */
function lerOuCriarClienteId() {
  const salvo = lerArmazenado("loja.clienteId");
  if (salvo) return salvo;

  const novo = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `cli-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  guardar("loja.clienteId", novo);
  return novo;
}

/** Chama a API do Gateway e devolve o JSON da resposta; erro vira exceção com a mensagem da loja. */
async function chamar(metodo, caminho, corpo) {
  let resposta;
  try {
    resposta = await fetch(API + caminho, {
      method: metodo,
      headers: corpo ? { "Content-Type": "application/json" } : undefined,
      body: corpo ? JSON.stringify(corpo) : undefined,
    });
  } catch {
    throw new Error("Não consegui falar com a loja. O Gateway está no ar?");
  }

  const dados = await resposta.json().catch(() => null);
  if (!resposta.ok) {
    throw new Error(dados?.erro ?? `A loja respondeu ${resposta.status}.`);
  }
  return dados;
}

function avisar(texto, tom = "andamento") {
  const aviso = el("div", { class: `aviso ${tom}`, role: "status" }, texto);
  $("avisos").append(aviso);
  setTimeout(() => aviso.remove(), 6000);
}

function marcarConexao(situacao) {
  const textos = { conectado: "Tempo real conectado", reconectando: "Reconectando…" };
  $("conexao").dataset.estado = situacao;
  $("conexao-texto").textContent = textos[situacao];
}

async function carregarProdutos() {
  try {
    estado.produtos = await chamar("GET", "/produtos");
  } catch (erro) {
    $("produtos").replaceChildren(el("p", { class: "vazio" }, erro.message));
    return;
  }

  for (const [id, linha] of estado.carrinho) {
    const atual = estado.produtos.find((produto) => produto.id === id);
    if (atual) estado.carrinho.set(id, { ...linha, produto: atual });
  }
  desenharCatalogo();
  desenharCarrinho();
}

function desenharFiltros() {
  const opcoes = [{ codigo: "", nome: "Todas" }, ...CATEGORIAS];
  $("filtros").replaceChildren(...opcoes.map((categoria) =>
    el("button", {
      type: "button",
      class: "filtro",
      "aria-pressed": String(estado.filtro === categoria.codigo),
      onclick: () => {
        estado.filtro = categoria.codigo;
        desenharFiltros();
        desenharCatalogo();
      },
    }, categoria.codigo ? `${categoria.codigo} · ${categoria.nome}` : categoria.nome)));
}

function desenharCatalogo() {
  const visiveis = estado.produtos.filter((produto) => !estado.filtro || produto.categoria === estado.filtro);
  if (visiveis.length === 0) {
    $("produtos").replaceChildren(el("p", { class: "vazio" }, "Nenhum produto com estoque nesta categoria."));
    return;
  }
  $("produtos").replaceChildren(...visiveis.map(cartaoDeProduto));
}

function cartaoDeProduto(produto) {
  const emPromocao = produto.preco_original > produto.preco;
  const desconto = emPromocao ? Math.round((1 - produto.preco / produto.preco_original) * 100) : 0;

  return el("article", { class: emPromocao ? "produto em-promocao" : "produto" },
    el("span", { class: "etiqueta" }, `Categoria ${produto.categoria}`),
    emPromocao && el("span", { class: "selo-promocao" }, `Promoção −${desconto}%`),
    el("h3", {}, produto.nome),
    el("p", { class: "preco" },
      emPromocao && el("s", { class: "preco-antigo" }, moeda.format(produto.preco_original)),
      moeda.format(produto.preco)),
    el("p", { class: "estoque" }, `${produto.quantidade} em estoque`),
    el("button", { type: "button", class: "botao", onclick: () => adicionar(produto) }, "Adicionar ao carrinho"));
}

function adicionar(produto) {
  const linha = estado.carrinho.get(produto.id);
  estado.carrinho.set(produto.id, { produto, quantidade: (linha?.quantidade ?? 0) + 1 });
  desenharCarrinho();
  avisar(`${produto.nome} no carrinho.`, "sucesso");
}

function alterarQuantidade(produtoId, delta) {
  const linha = estado.carrinho.get(produtoId);
  if (!linha) return;

  const quantidade = linha.quantidade + delta;
  if (quantidade <= 0) {
    estado.carrinho.delete(produtoId);
  } else {
    estado.carrinho.set(produtoId, { ...linha, quantidade });
  }
  desenharCarrinho();
}

function desenharCarrinho() {
  const linhas = [...estado.carrinho.values()];
  const total = linhas.reduce((soma, linha) => soma + linha.produto.preco * linha.quantidade, 0);

  $("itens-carrinho").replaceChildren(...(linhas.length === 0
    ? [el("li", { class: "vazio" }, "Seu carrinho está vazio.")]
    : linhas.map(linhaDoCarrinho)));
  $("total-carrinho").textContent = moeda.format(total);
  $("finalizar").disabled = linhas.length === 0 || estado.enviando;
  $("finalizar").textContent = estado.enviando ? "Enviando…" : "Finalizar pedido";
}

function linhaDoCarrinho({ produto, quantidade }) {
  const acimaDoEstoque = quantidade > produto.quantidade;
  return el("li", { class: "linha-carrinho" },
    el("div", {},
      el("strong", {}, produto.nome),
      el("span", { class: "subtotal" }, moeda.format(produto.preco * quantidade)),
      acimaDoEstoque && el("span", { class: "alerta" },
        `Acima das ${produto.quantidade} unidades em estoque: quem decide é o Estoque.`)),
    el("div", { class: "quantidade" },
      el("button", {
        type: "button",
        class: "mini",
        "aria-label": `Tirar uma unidade de ${produto.nome}`,
        onclick: () => alterarQuantidade(produto.id, -1),
      }, "−"),
      el("span", {}, String(quantidade)),
      el("button", {
        type: "button",
        class: "mini",
        "aria-label": `Pôr mais uma unidade de ${produto.nome}`,
        onclick: () => alterarQuantidade(produto.id, 1),
      }, "+")));
}

/**
 * Abre a aba do pagamento ainda dentro do clique, que é quando o navegador
 * permite abrir janelas; ela recebe a URL do Mock quando o SSE trouxer.
 */
function abrirAbaDePagamento() {
  const aba = window.open("", "_blank");
  if (!aba) return null;

  aba.document.title = "Pagamento";
  aba.document.body.innerHTML =
    '<p style="font-family:system-ui;padding:24px">Aguardando a loja gerar o link de pagamento…</p>';
  return aba;
}

async function finalizarPedido() {
  if (estado.carrinho.size === 0 || estado.enviando) return;

  const aba = abrirAbaDePagamento();
  estado.enviando = true;
  desenharCarrinho();

  try {
    const pedido = await chamar("POST", "/pedidos", {
      cliente_id: estado.clienteId,
      cliente: $("nome-cliente").value.trim(),
      itens: [...estado.carrinho.values()].map(({ produto, quantidade }) => ({ produto_id: produto.id, quantidade })),
    });

    if (aba) {
      estado.abasDePagamento.set(pedido.id, aba);
    } else {
      avisar("O navegador bloqueou a nova aba. Use o botão “Ir para o pagamento” no pedido.", "atencao");
    }
    estado.carrinho.clear();
    atualizarPedido(pedido, false);
    avisar(`Pedido ${pedido.id} recebido. O status muda sozinho em “Meus pedidos”.`);
  } catch (erro) {
    aba?.close();
    avisar(erro.message, "erro");
  } finally {
    estado.enviando = false;
    desenharCarrinho();
  }
}

/**
 * Leva a aba aberta no clique até a página do Mock quando a URL chega, ou
 * explica nela por que o pedido não seguiu para o pagamento.
 */
function encaminharPagamento(pedido) {
  const aba = estado.abasDePagamento.get(pedido.id);
  if (!aba) return;

  if (aba.closed) {
    estado.abasDePagamento.delete(pedido.id);
    return;
  }

  if (pedido.status === "aguardando_pagamento" && pedido.url_checkout) {
    aba.location.href = pedido.url_checkout;
    estado.abasDePagamento.delete(pedido.id);
    return;
  }

  if (ENCERRADOS.has(pedido.status)) {
    aba.document.body.textContent =
      `O pedido ${pedido.id} não seguiu para o pagamento: ${ROTULOS_STATUS[pedido.status]}. ${pedido.detalhe}`;
    estado.abasDePagamento.delete(pedido.id);
  }
}

/**
 * Guarda a versão mais nova do pedido. Como o Gateway manda sempre o pedido
 * inteiro, a versão com o histórico mais longo é a mais recente.
 */
function atualizarPedido(pedido, notificar) {
  const anterior = estado.pedidos.get(pedido.id);

  if (!anterior || pedido.historico.length >= anterior.historico.length) {
    estado.pedidos.set(pedido.id, pedido);
    if (notificar && anterior?.status !== pedido.status) {
      avisar(`${pedido.id}: ${ROTULOS_STATUS[pedido.status] ?? pedido.status}`, TOM_STATUS[pedido.status]);
    }
    desenharPedidos();
  }

  encaminharPagamento(estado.pedidos.get(pedido.id));
}

async function carregarPedidos() {
  let lista;
  try {
    lista = await chamar("GET", `/pedidos?cliente_id=${encodeURIComponent(estado.clienteId)}`);
  } catch (erro) {
    avisar(erro.message, "erro");
    return;
  }

  const ids = new Set(lista.map((pedido) => pedido.id));
  for (const id of estado.pedidos.keys()) {
    if (!ids.has(id)) estado.pedidos.delete(id);
  }
  for (const pedido of lista) atualizarPedido(pedido, false);
  desenharPedidos();
}

async function cancelarPedido(pedidoId) {
  if (!confirm(`Cancelar o pedido ${pedidoId}? O que estava reservado volta para o estoque.`)) return;

  try {
    const pedido = await chamar("DELETE",
      `/pedidos/${encodeURIComponent(pedidoId)}?cliente_id=${encodeURIComponent(estado.clienteId)}`);
    atualizarPedido(pedido, true);
  } catch (erro) {
    avisar(erro.message, "erro");
  }
}

function desenharPedidos() {
  const pedidos = [...estado.pedidos.values()]
    .sort((a, b) => new Date(b.criado_em) - new Date(a.criado_em));

  $("lista-pedidos").replaceChildren(...(pedidos.length === 0
    ? [el("p", { class: "vazio" }, "Você ainda não fez pedidos.")]
    : pedidos.map(cartaoDePedido)));
}

function cartaoDePedido(pedido) {
  const aguardandoPagamento = pedido.status === "aguardando_pagamento" && pedido.url_checkout;

  return el("article", { class: "pedido" },
    el("header", { class: "pedido-topo" },
      el("div", {},
        el("strong", {}, pedido.id),
        el("span", { class: "quando" }, horario.format(new Date(pedido.criado_em)))),
      el("span", { class: `selo ${TOM_STATUS[pedido.status] ?? ""}` }, ROTULOS_STATUS[pedido.status] ?? pedido.status)),
    linhaDoTempo(pedido),
    el("p", { class: "detalhe" }, pedido.detalhe),
    el("ul", { class: "itens-pedido" }, ...pedido.itens.map((item) =>
      el("li", {},
        el("span", {}, `${item.quantidade} × ${item.nome}`),
        el("span", {}, moeda.format(item.preco_unitario * item.quantidade))))),
    el("div", { class: "pedido-rodape" },
      el("strong", {}, `Total ${moeda.format(pedido.total)}`),
      el("div", { class: "acoes" },
        aguardandoPagamento && el("a", {
          class: "botao primario",
          href: pedido.url_checkout,
          target: "_blank",
          rel: "noopener",
        }, "Ir para o pagamento"),
        !ENCERRADOS.has(pedido.status) && el("button", {
          type: "button",
          class: "botao perigo",
          onclick: () => cancelarPedido(pedido.id),
        }, "Cancelar pedido"))),
    el("details", {
      class: "historico",
      open: estado.historicosAbertos.has(pedido.id),
      ontoggle: (evento) => {
        if (evento.target.open) {
          estado.historicosAbertos.add(pedido.id);
        } else {
          estado.historicosAbertos.delete(pedido.id);
        }
      },
    },
      el("summary", {}, "Histórico"),
      el("ol", {}, ...pedido.historico.map((marca) =>
        el("li", {},
          el("time", {}, horario.format(new Date(marca.quando))),
          ` ${ROTULOS_STATUS[marca.status] ?? marca.status}`,
          marca.detalhe && el("span", { class: "quando" }, ` — ${marca.detalhe}`))))));
}

function situacaoDaEtapa(feita, falhou, atual) {
  if (feita) return "feito";
  if (falhou) return "falhou";
  if (atual) return "atual";
  return "pendente";
}

function linhaDoTempo(pedido) {
  const vistos = new Set(pedido.historico.map((marca) => marca.status));
  const status = pedido.status;

  const etapas = [
    ["Pedido criado", "feito"],
    ["Estoque", situacaoDaEtapa(vistos.has("estoque_confirmado"), status === "sem_estoque", status === "aguardando_estoque")],
    ["Pagamento", situacaoDaEtapa(vistos.has("pagamento_aprovado"), status === "pagamento_recusado",
      status === "estoque_confirmado" || status === "aguardando_pagamento")],
    ["Envio", situacaoDaEtapa(vistos.has("enviado"), false, status === "pagamento_aprovado")],
  ];

  return el("ol", { class: "etapas", "aria-label": "Andamento do pedido" },
    ...etapas.map(([rotulo, situacao]) => el("li", { class: situacao }, rotulo)));
}

/** Mantém a conexão SSE com o Gateway; o navegador reconecta sozinho quando ela cai. */
function conectarEventos() {
  const fonte = new EventSource(`${API}/eventos?cliente_id=${encodeURIComponent(estado.clienteId)}`);

  fonte.addEventListener("open", () => {
    marcarConexao("conectado");
    carregarPedidos();
  });

  fonte.addEventListener("pedido", (mensagem) => {
    atualizarPedido(JSON.parse(mensagem.data), true);
    carregarProdutos();
  });

  fonte.addEventListener("error", () => {
    marcarConexao("reconectando");
    if (fonte.readyState === EventSource.CLOSED) {
      setTimeout(conectarEventos, 3000);
    }
  });
}

function desenharCategoriasDePromocao() {
  $("categorias").append(...CATEGORIAS.map((categoria) =>
    el("label", { class: "opcao" },
      el("input", { type: "checkbox", name: "categoria", value: categoria.codigo }),
      `${categoria.codigo} · ${categoria.nome}`)));
}

function mensagemPromocoes(texto, tom) {
  const mensagem = $("mensagem-promocoes");
  mensagem.textContent = texto;
  mensagem.className = `mensagem ${tom}`;
}

async function inscrever(evento) {
  evento.preventDefault();

  const campo = $("email");
  if (!campo.reportValidity()) return;

  const categorias = [...document.querySelectorAll('input[name="categoria"]:checked')].map((caixa) => caixa.value);
  if (categorias.length === 0) {
    mensagemPromocoes("Escolha pelo menos uma categoria.", "erro");
    return;
  }

  try {
    const dados = await chamar("POST", "/interesses", { email: campo.value.trim(), categorias });
    guardar("loja.email", dados.email);
    mensagemPromocoes(
      `Pronto: ${dados.email} vai receber as promoções das categorias ${dados.categorias.join(", ")}.`, "sucesso");
  } catch (erro) {
    mensagemPromocoes(erro.message, "erro");
  }
}

async function cancelarInscricao() {
  const campo = $("email");
  if (!campo.reportValidity()) return;

  try {
    const dados = await chamar("DELETE", `/interesses/${encodeURIComponent(campo.value.trim())}`);
    mensagemPromocoes(`${dados.email} não vai mais receber promoções.`, "sucesso");
  } catch (erro) {
    mensagemPromocoes(erro.message, "erro");
  }
}

function iniciar() {
  $("cliente-id").textContent = estado.clienteId;

  const nome = $("nome-cliente");
  nome.value = lerArmazenado("loja.nome") ?? "";
  nome.addEventListener("change", () => guardar("loja.nome", nome.value.trim()));
  $("email").value = lerArmazenado("loja.email") ?? "";

  $("finalizar").addEventListener("click", finalizarPedido);
  $("form-promocoes").addEventListener("submit", inscrever);
  $("cancelar-inscricao").addEventListener("click", cancelarInscricao);

  desenharFiltros();
  desenharCategoriasDePromocao();
  desenharCarrinho();
  desenharPedidos();
  carregarProdutos();
  conectarEventos();
}

iniciar();
