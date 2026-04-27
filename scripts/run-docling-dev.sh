#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_HOST="${DOCLING_HOST:-127.0.0.1}"
DOCLING_PORT="${DOCLING_PORT:-5001}"

if [ -z "${DOCLING_VENV:-}" ]; then
	if [ -x "$ROOT/.local/docling/bin/docling-serve" ]; then
		DOCLING_VENV="$ROOT/.local/docling"
	elif [ -x /opt/zora/docling/bin/docling-serve ]; then
		DOCLING_VENV="/opt/zora/docling"
	else
		DOCLING_VENV="$ROOT/.local/docling"
	fi
fi

if [ ! -x "$DOCLING_VENV/bin/docling-serve" ]; then
	echo "Docling is not installed at $DOCLING_VENV. Run scripts/bootstrap-macos.sh or scripts/bootstrap-ubuntu.sh first." >&2
	exit 1
fi

exec "$DOCLING_VENV/bin/docling-serve" run --host "$DOCLING_HOST" --port "$DOCLING_PORT" --enable-ui
