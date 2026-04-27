#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_VENV="${DOCLING_VENV:-$ROOT/.local/docling}"

status=0

check_command() {
	if command -v "$1" >/dev/null 2>&1; then
		echo "ok: $1 ($(command -v "$1"))"
	else
		echo "missing: $1" >&2
		status=1
	fi
}

check_url() {
	name="$1"
	url="$2"
	if command -v curl >/dev/null 2>&1 && curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
		echo "ok: $name ($url)"
	else
		echo "warn: $name is not reachable at $url"
	fi
}

check_command go
check_command goreleaser
check_command python3

if [ -x "$DOCLING_VENV/bin/docling-serve" ]; then
	echo "ok: docling-serve ($DOCLING_VENV/bin/docling-serve)"
else
	echo "warn: docling-serve not installed at $DOCLING_VENV"
fi

mkdir -p "$ROOT/.local/runtime" "$ROOT/.local/archive" "$ROOT/.local/inbox"

check_url "Zora" "http://127.0.0.1:8787/healthz"
check_url "Docling" "http://127.0.0.1:5001/docs"

if [ "${1:-}" = "--test" ]; then
	(cd "$ROOT" && CGO_ENABLED=0 go test ./...)
fi

exit "$status"
