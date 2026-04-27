#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_VENV="${DOCLING_VENV:-$ROOT/.local/docling}"

if ! command -v brew >/dev/null 2>&1; then
	echo "Homebrew is required. Install it from https://brew.sh/ and rerun this script." >&2
	exit 1
fi

brew bundle check --file "$ROOT/Brewfile" || brew bundle install --file "$ROOT/Brewfile"

mkdir -p \
	"$ROOT/.local/runtime" \
	"$ROOT/.local/archive" \
	"$ROOT/.local/inbox" \
	"$ROOT/.local/docling"

if ! command -v uv >/dev/null 2>&1; then
	echo "uv was not found after brew bundle. Check your Homebrew PATH and rerun this script." >&2
	exit 1
fi

if [ ! -x "$DOCLING_VENV/bin/python" ]; then
	uv venv "$DOCLING_VENV"
fi

uv pip install --python "$DOCLING_VENV/bin/python" "docling-serve[ui]"

cat <<MSG
Zora macOS development setup is ready.

Optional: pull the default local LLM for action item generation:
  make llm-pull

Run the local development stack:
  make dev
MSG
