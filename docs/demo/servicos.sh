#!/usr/bin/env bash
# Sobe e derruba os processos da loja para gravar a demonstração.
#
#   docs/demo/servicos.sh subir
#   docs/demo/servicos.sh parar
#
#   FRONTEND_PORTA=5174 docs/demo/servicos.sh subir
#   PROMOCOES_INTERVALO=30s docs/demo/servicos.sh subir
set -euo pipefail

RAIZ="$(cd "$(dirname "$0")/../.." && pwd)"
EXECUCAO="$RAIZ/docs/demo/execucao"
LOGS="$EXECUCAO/logs"
PIDS="$EXECUCAO/pids"
FRONTEND_PORTA="${FRONTEND_PORTA:-5173}"
TEMPO_LIMITE_SEGUNDOS=180
ORDEM_DE_SUBIDA=(estoque mock-pagamento pagamento entrega promocoes principal frontend)

unset RESEND_API_KEY
export ESTOQUE_BANCO="$EXECUCAO/estoque.db"
export PROMOCOES_BANCO="$EXECUCAO/promocoes.db"
export PROMOCOES_INTERVALO="${PROMOCOES_INTERVALO:-20s}"
export FRONTEND_ORIGEM="http://localhost:$FRONTEND_PORTA,http://127.0.0.1:$FRONTEND_PORTA"
export PYTHONUNBUFFERED=1

no_ar() {
  [[ -f "$PIDS/$1.pid" ]] && kill -0 "$(cat "$PIDS/$1.pid")" 2>/dev/null
}

falhar() {
  echo "ERRO: $1" >&2
  [[ -f "$LOGS/$2.log" ]] && tail -n 20 "$LOGS/$2.log" >&2
  parar
  exit 1
}

# iniciar NOME DIRETORIO COMANDO... roda o comando numa sessão própria, para
# o parar encerrar o grupo inteiro (go run e o binário que ele compila).
iniciar() {
  local nome="$1" diretorio="$2"
  shift 2
  (cd "$diretorio" && exec setsid "$@") >"$LOGS/$nome.log" 2>&1 </dev/null &
  echo $! >"$PIDS/$nome.pid"
}

esperar() {
  local nome="$1" descricao="$2" condicao="$3"
  local limite=$((SECONDS + TEMPO_LIMITE_SEGUNDOS))
  until eval "$condicao"; do
    no_ar "$nome" || falhar "$nome encerrou antes de $descricao" "$nome"
    ((SECONDS < limite)) || falhar "$nome não ficou pronto em ${TEMPO_LIMITE_SEGUNDOS}s ($descricao)" "$nome"
    sleep 0.5
  done
  echo "  $nome: $descricao"
}

porta_aberta() {
  (: </dev/tcp/127.0.0.1/"$1") 2>/dev/null
}

fila_consumida() {
  grep -qF "aguardando eventos na fila $2" "$LOGS/$1.log"
}

broker_pronto() {
  curl -sf -u guest:guest -o /dev/null http://localhost:15672/api/overview && porta_aberta 5672
}

subir_broker() {
  if [[ "$(docker inspect -f '{{.State.Running}}' sd-rabbitmq 2>/dev/null)" == "true" ]]; then
    echo "  broker: já estava no ar, fica no ar no parar"
  else
    (cd "$RAIZ" && docker compose up -d >/dev/null)
    touch "$PIDS/broker.iniciado"
  fi
  setsid docker logs -f sd-rabbitmq >"$LOGS/broker.log" 2>&1 </dev/null &
  echo $! >"$PIDS/broker-log.pid"

  local limite=$((SECONDS + TEMPO_LIMITE_SEGUNDOS))
  until broker_pronto; do
    ((SECONDS < limite)) || falhar "o RabbitMQ não respondeu em ${TEMPO_LIMITE_SEGUNDOS}s" broker
    sleep 1
  done
  echo "  broker: painel e AMQP respondendo"
}

subir() {
  for nome in "${ORDEM_DE_SUBIDA[@]}"; do
    if no_ar "$nome"; then
      echo "ERRO: $nome já está no ar; rode '$0 parar' antes" >&2
      exit 1
    fi
  done
  if porta_aberta "$FRONTEND_PORTA"; then
    echo "ERRO: a porta $FRONTEND_PORTA já está em uso; escolha outra com FRONTEND_PORTA=..." >&2
    exit 1
  fi

  mkdir -p "$LOGS" "$PIDS"
  rm -f "$ESTOQUE_BANCO" "$PROMOCOES_BANCO" "$LOGS"/*.log

  echo "Subindo (logs em $LOGS):"
  subir_broker

  iniciar estoque "$RAIZ" go run ./cmd/estoque
  esperar estoque "fila estoque.pedidos e porta 8081" "fila_consumida estoque estoque.pedidos && porta_aberta 8081"

  iniciar mock-pagamento "$RAIZ/mock-pagamento" .venv/bin/python app.py
  esperar mock-pagamento "porta 8090" "porta_aberta 8090"

  iniciar pagamento "$RAIZ" go run ./cmd/pagamento
  esperar pagamento "fila pagamento.estoque_ok e porta 8082" "fila_consumida pagamento pagamento.estoque_ok && porta_aberta 8082"

  iniciar entrega "$RAIZ" go run ./cmd/entrega
  esperar entrega "fila entrega.pagamentos" "fila_consumida entrega entrega.pagamentos"

  iniciar promocoes "$RAIZ" go run ./cmd/promocoes
  esperar promocoes "fila promocoes.interesses, promoção a cada $PROMOCOES_INTERVALO" "fila_consumida promocoes promocoes.interesses"

  iniciar principal "$RAIZ" go run ./cmd/principal
  esperar principal "fila principal.status e porta 8080" "fila_consumida principal principal.status && porta_aberta 8080"

  iniciar frontend "$RAIZ/web" python3 -m http.server "$FRONTEND_PORTA"
  esperar frontend "porta $FRONTEND_PORTA" "porta_aberta $FRONTEND_PORTA"

  echo "Pronto: frontend em http://localhost:$FRONTEND_PORTA, painel em http://localhost:15672"
}

encerrar_grupo() {
  local nome="$1" pid
  pid="$(cat "$PIDS/$nome.pid")"
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid"
    for _ in $(seq 20); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    kill -0 "$pid" 2>/dev/null && kill -KILL -- "-$pid"
    echo "  $nome encerrado"
  fi
  rm -f "$PIDS/$nome.pid"
}

parar() {
  [[ -d "$PIDS" ]] || return 0
  for ((i = ${#ORDEM_DE_SUBIDA[@]} - 1; i >= 0; i--)); do
    [[ -f "$PIDS/${ORDEM_DE_SUBIDA[i]}.pid" ]] && encerrar_grupo "${ORDEM_DE_SUBIDA[i]}"
  done
  if [[ -f "$PIDS/broker-log.pid" ]]; then
    kill "$(cat "$PIDS/broker-log.pid")" 2>/dev/null || true
    rm -f "$PIDS/broker-log.pid"
  fi
  if [[ -f "$PIDS/broker.iniciado" ]]; then
    (cd "$RAIZ" && docker compose stop >/dev/null 2>&1)
    rm -f "$PIDS/broker.iniciado"
    echo "  broker parado"
  fi
}

case "${1:-}" in
  subir) subir ;;
  parar) parar ;;
  *)
    echo "uso: $0 subir|parar" >&2
    exit 2
    ;;
esac
