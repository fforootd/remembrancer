#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_VENV="${DOCLING_VENV:-$ROOT/.local/docling}"
ZORA_URL="${ZORA_URL:-http://127.0.0.1:8787/healthz}"
DOCLING_URL="${DOCLING_URL:-http://127.0.0.1:5001/docs}"
OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434/api/tags}"
ZORA_LLM_MODEL="${ZORA_LLM_MODEL:-qwen3.5:9b-q4_K_M}"

status=0
strict=0
run_tests=0

usage() {
	cat <<'MSG'
usage: scripts/doctor.sh [--strict] [--test]

Options:
  --strict  Treat warnings, such as stopped local services, as failures.
  --test    Run CGO_ENABLED=0 go test ./... after environment checks.
MSG
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--strict)
			strict=1
			;;
		--test)
			run_tests=1
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
	shift
done

warn() {
	echo "warn: $*"
	if [ "$strict" = "1" ]; then
		status=1
	fi
}

check_required_command() {
	if command -v "$1" >/dev/null 2>&1; then
		echo "ok: $1 ($(command -v "$1"))"
	else
		echo "missing: $1" >&2
		status=1
	fi
}

check_optional_command() {
	if command -v "$1" >/dev/null 2>&1; then
		echo "ok: $1 ($(command -v "$1"))"
	else
		warn "$1 is not installed"
	fi
}

check_url() {
	local name="$1"
	local url="$2"
	if command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
		echo "ok: $name ($url)"
	else
		warn "$name is not reachable at $url"
	fi
}

url_ok() {
	local url="$1"
	command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "$url" >/dev/null 2>&1
}

check_ollama_model() {
	local model="$1"
	if ! url_ok "$OLLAMA_URL"; then
		return
	fi
	if curl -fsS --max-time 2 "$OLLAMA_URL" 2>/dev/null | grep -F "\"name\":\"$model\"" >/dev/null 2>&1; then
		echo "ok: Ollama model $model"
	else
		warn "Ollama model $model is not installed; run make llm-pull"
	fi
}

check_required_command go
check_required_command python3
check_required_command curl
check_optional_command uv
check_optional_command ollama
check_optional_command goreleaser
check_optional_command sqlite3

if [ -x "$DOCLING_VENV/bin/docling-serve" ]; then
	echo "ok: docling-serve ($DOCLING_VENV/bin/docling-serve)"
else
	warn "docling-serve not installed at $DOCLING_VENV"
fi

mkdir -p "$ROOT/.local/runtime" "$ROOT/.local/archive" "$ROOT/.local/inbox"
echo "ok: local paths (.local/runtime, .local/archive, .local/inbox)"

check_url "Zora" "$ZORA_URL"
check_url "Docling" "$DOCLING_URL"
check_url "Ollama" "$OLLAMA_URL"
check_ollama_model "$ZORA_LLM_MODEL"

if [ "$run_tests" = "1" ]; then
	(cd "$ROOT" && CGO_ENABLED=0 go test ./...)
fi

exit "$status"
