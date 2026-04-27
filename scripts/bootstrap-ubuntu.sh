#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCLING_VENV="${DOCLING_VENV:-/opt/zora/docling}"

if ! command -v apt-get >/dev/null 2>&1; then
	echo "This bootstrap script is intended for Ubuntu or Debian hosts with apt-get." >&2
	exit 1
fi

sudo apt-get update
sudo apt-get install -y \
	build-essential \
	ca-certificates \
	curl \
	git \
	golang-go \
	python3 \
	python3-pip \
	python3-venv \
	pipx \
	ripgrep \
	sqlite3

export PATH="$HOME/.local/bin:$PATH"

if ! command -v uv >/dev/null 2>&1; then
	python3 -m pipx install uv
fi

if ! getent group zora >/dev/null; then
	sudo groupadd --system zora
fi

if ! id zora >/dev/null 2>&1; then
	sudo useradd --system --gid zora --home-dir /var/lib/zora --shell /usr/sbin/nologin zora
fi

sudo install -d -o zora -g zora -m 0750 \
	/var/lib/zora \
	/var/lib/zora/runtime \
	/var/lib/zora/archive \
	/var/lib/zora/inbox \
	/var/lib/zora/docling \
	/var/lib/zora/docling/artifacts

sudo install -d -o root -g zora -m 0750 /etc/zora
sudo install -d -o "$USER" -g zora -m 0755 /opt/zora

if [ ! -x "$DOCLING_VENV/bin/python" ]; then
	uv venv "$DOCLING_VENV"
fi

uv pip install --python "$DOCLING_VENV/bin/python" "docling-serve[ui]"

sudo chgrp -R zora "$DOCLING_VENV"
sudo chmod -R g+rX "$DOCLING_VENV"

if [ ! -f /etc/zora/config.yaml ]; then
	sudo install -o root -g zora -m 0640 "$ROOT/packaging/config/zora.yaml" /etc/zora/config.yaml
fi

if [ -d /lib/systemd/system ]; then
	sudo install -o root -g root -m 0644 "$ROOT/packaging/systemd/zora.service" /lib/systemd/system/zora.service
	sudo install -o root -g root -m 0644 "$ROOT/packaging/systemd/docling-serve.service" /lib/systemd/system/docling-serve.service
	sudo systemctl daemon-reload
fi

cat <<'MSG'
Zora Ubuntu setup is ready.

Start services when ready:
  sudo systemctl enable --now docling-serve
  sudo systemctl enable --now zora

Check status:
  systemctl status docling-serve
  systemctl status zora
MSG
