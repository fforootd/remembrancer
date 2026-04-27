#!/usr/bin/env bash
set -euo pipefail

OLLAMA_BIND="${OLLAMA_BIND:-${OLLAMA_HOST:-127.0.0.1:11434}}"

if ! command -v ollama >/dev/null 2>&1; then
	echo "Ollama is not installed. Run make setup, or install it from https://ollama.com/download." >&2
	exit 1
fi

export OLLAMA_HOST="$OLLAMA_BIND"
exec ollama serve
