#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ZORA_CONFIG="${ZORA_CONFIG:-$ROOT/config/example.yaml}"
cd "$ROOT"

exec go run ./cmd/zora serve --config "$ZORA_CONFIG"
