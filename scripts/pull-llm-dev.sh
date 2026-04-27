#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OLLAMA_BIND="${OLLAMA_BIND:-${OLLAMA_HOST:-127.0.0.1:11434}}"
OLLAMA_BASE_URL="${OLLAMA_BASE_URL:-http://$OLLAMA_BIND}"
OLLAMA_HEALTH_URL="${OLLAMA_HEALTH_URL:-$OLLAMA_BASE_URL/api/tags}"
ZORA_LLM_MODEL="${ZORA_LLM_MODEL:-qwen3.5:9b-q4_K_M}"

started_pid=""

cleanup() {
	trap - EXIT INT TERM
	if [ -n "$started_pid" ] && kill -0 "$started_pid" >/dev/null 2>&1; then
		kill "$started_pid" >/dev/null 2>&1 || true
		wait "$started_pid" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM

url_ok() {
	local url="$1"
	command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "$url" >/dev/null 2>&1
}

wait_for_url() {
	local url="$1"
	local timeout_seconds="$2"
	local deadline=$((SECONDS + timeout_seconds))

	while [ "$SECONDS" -lt "$deadline" ]; do
		if ! kill -0 "$started_pid" >/dev/null 2>&1; then
			set +e
			wait "$started_pid"
			local code=$?
			set -e
			echo "Ollama exited before becoming healthy." >&2
			exit "$code"
		fi
		if url_ok "$url"; then
			return 0
		fi
		sleep 1
	done

	echo "Ollama did not become reachable at $url within ${timeout_seconds}s" >&2
	exit 1
}

if ! command -v ollama >/dev/null 2>&1; then
	echo "Ollama is not installed. Run make setup, or install it from https://ollama.com/download." >&2
	exit 1
fi

if ! url_ok "$OLLAMA_HEALTH_URL"; then
	echo "starting temporary Ollama server"
	"$ROOT/scripts/run-ollama-dev.sh" &
	started_pid=$!
	wait_for_url "$OLLAMA_HEALTH_URL" 30
fi

export OLLAMA_HOST="$OLLAMA_BIND"
ollama pull "$ZORA_LLM_MODEL"
