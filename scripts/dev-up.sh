#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_HOST="${DOCLING_HOST:-127.0.0.1}"
DOCLING_PORT="${DOCLING_PORT:-5001}"
DOCLING_BASE_URL="${DOCLING_BASE_URL:-http://$DOCLING_HOST:$DOCLING_PORT}"
DOCLING_HEALTH_URL="${DOCLING_HEALTH_URL:-$DOCLING_BASE_URL/docs}"
OLLAMA_BIND="${OLLAMA_BIND:-${OLLAMA_HOST:-127.0.0.1:11434}}"
OLLAMA_BASE_URL="${OLLAMA_BASE_URL:-http://$OLLAMA_BIND}"
OLLAMA_HEALTH_URL="${OLLAMA_HEALTH_URL:-$OLLAMA_BASE_URL/api/tags}"
ZORA_LLM_MODEL="${ZORA_LLM_MODEL:-qwen3.5:9b-q4_K_M}"
ZORA_HEALTH_URL="${ZORA_HEALTH_URL:-http://127.0.0.1:8787/healthz}"

pids=()
started_pid=""

cleanup() {
	trap - EXIT INT TERM
	for pid in "${pids[@]:-}"; do
		if kill -0 "$pid" >/dev/null 2>&1; then
			kill "$pid" >/dev/null 2>&1 || true
		fi
	done
	for pid in "${pids[@]:-}"; do
		wait "$pid" >/dev/null 2>&1 || true
	done
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM

url_ok() {
	local url="$1"
	command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "$url" >/dev/null 2>&1
}

ollama_model_ready() {
	local model="$1"
	command -v curl >/dev/null 2>&1 &&
		curl -fsS --max-time 2 "$OLLAMA_HEALTH_URL" 2>/dev/null |
			grep -F "\"name\":\"$model\"" >/dev/null 2>&1
}

warn_missing_model() {
	local model="$1"
	if ollama_model_ready "$model"; then
		echo "ok: Ollama model is available: $model"
	else
		echo "warn: Ollama is running, but model '$model' is not installed."
		echo "      Pull it with: make llm-pull"
	fi
}

wait_for_url() {
	local name="$1"
	local url="$2"
	local pid="$3"
	local timeout_seconds="$4"
	local deadline=$((SECONDS + timeout_seconds))

	while [ "$SECONDS" -lt "$deadline" ]; do
		if ! kill -0 "$pid" >/dev/null 2>&1; then
			set +e
			wait "$pid"
			local code=$?
			set -e
			echo "$name exited before becoming healthy." >&2
			exit "$code"
		fi
		if url_ok "$url"; then
			echo "ok: $name is reachable at $url"
			return 0
		fi
		sleep 1
	done

	echo "warn: $name did not become reachable at $url within ${timeout_seconds}s"
	return 1
}

start_service() {
	local name="$1"
	shift
	echo "starting $name"
	"$@" &
	started_pid=$!
	pids+=("$started_pid")
	echo "$name pid: $started_pid"
}

mkdir -p "$ROOT/.local/runtime" "$ROOT/.local/archive" "$ROOT/.local/inbox"

docling_pid=""
if url_ok "$DOCLING_HEALTH_URL"; then
	echo "ok: existing Docling is reachable at $DOCLING_HEALTH_URL"
else
	start_service "Docling" "$ROOT/scripts/run-docling-dev.sh"
	docling_pid="$started_pid"
	wait_for_url "Docling" "$DOCLING_HEALTH_URL" "$docling_pid" 120 || true
fi

ollama_pid=""
if url_ok "$OLLAMA_HEALTH_URL"; then
	echo "ok: existing Ollama is reachable at $OLLAMA_HEALTH_URL"
else
	start_service "Ollama" "$ROOT/scripts/run-ollama-dev.sh"
	ollama_pid="$started_pid"
	wait_for_url "Ollama" "$OLLAMA_HEALTH_URL" "$ollama_pid" 30 || true
fi
if url_ok "$OLLAMA_HEALTH_URL"; then
	warn_missing_model "$ZORA_LLM_MODEL"
fi

zora_pid=""
if url_ok "$ZORA_HEALTH_URL"; then
	echo "ok: existing Zora is reachable at $ZORA_HEALTH_URL"
else
	start_service "Zora" "$ROOT/scripts/run-zora-dev.sh"
	zora_pid="$started_pid"
	wait_for_url "Zora" "$ZORA_HEALTH_URL" "$zora_pid" 30 || true
fi

cat <<MSG

Local stack is up.

Zora:
  http://127.0.0.1:8787/
  http://127.0.0.1:8787/healthz

Docling:
  $DOCLING_BASE_URL/docs
  $DOCLING_BASE_URL/ui

Ollama:
  $OLLAMA_BASE_URL
  model: $ZORA_LLM_MODEL

Drop files into:
  $ROOT/.local/inbox

Press Ctrl-C to stop services started by this script.
MSG

while :; do
	if [ -n "$docling_pid" ]; then
		if ! kill -0 "$docling_pid" >/dev/null 2>&1; then
			set +e
			wait "$docling_pid"
			code=$?
			set -e
			exit "$code"
		fi
	elif ! url_ok "$DOCLING_HEALTH_URL"; then
		echo "warn: existing Docling is no longer reachable at $DOCLING_HEALTH_URL; starting Docling"
		start_service "Docling" "$ROOT/scripts/run-docling-dev.sh"
		docling_pid="$started_pid"
		wait_for_url "Docling" "$DOCLING_HEALTH_URL" "$docling_pid" 120 || true
	fi

	if [ -n "$ollama_pid" ]; then
		if ! kill -0 "$ollama_pid" >/dev/null 2>&1; then
			set +e
			wait "$ollama_pid"
			code=$?
			set -e
			exit "$code"
		fi
	elif ! url_ok "$OLLAMA_HEALTH_URL"; then
		echo "warn: existing Ollama is no longer reachable at $OLLAMA_HEALTH_URL; starting Ollama"
		start_service "Ollama" "$ROOT/scripts/run-ollama-dev.sh"
		ollama_pid="$started_pid"
		wait_for_url "Ollama" "$OLLAMA_HEALTH_URL" "$ollama_pid" 30 || true
		if url_ok "$OLLAMA_HEALTH_URL"; then
			warn_missing_model "$ZORA_LLM_MODEL"
		fi
	fi

	if [ -n "$zora_pid" ]; then
		if ! kill -0 "$zora_pid" >/dev/null 2>&1; then
			set +e
			wait "$zora_pid"
			code=$?
			set -e
			exit "$code"
		fi
	elif ! url_ok "$ZORA_HEALTH_URL"; then
		echo "warn: existing Zora is no longer reachable at $ZORA_HEALTH_URL; starting Zora"
		start_service "Zora" "$ROOT/scripts/run-zora-dev.sh"
		zora_pid="$started_pid"
		wait_for_url "Zora" "$ZORA_HEALTH_URL" "$zora_pid" 30 || true
	fi

	sleep 1
done
