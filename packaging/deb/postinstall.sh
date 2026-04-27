#!/bin/sh
set -e

if ! getent group zora >/dev/null; then
	groupadd --system zora
fi

if ! id zora >/dev/null 2>&1; then
	useradd --system --gid zora --home-dir /var/lib/zora --shell /usr/sbin/nologin zora
fi

install -d -o zora -g zora -m 0750 \
	/var/lib/zora \
	/var/lib/zora/runtime \
	/var/lib/zora/archive \
	/var/lib/zora/inbox \
	/var/lib/zora/docling \
	/var/lib/zora/docling/artifacts

install -d -o root -g zora -m 0750 /etc/zora /opt/zora

if [ -f /etc/zora/config.yaml ]; then
	chgrp zora /etc/zora/config.yaml || true
	chmod 0640 /etc/zora/config.yaml || true
fi

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi

cat <<'MSG'
Zora installed.

Next steps:
  1. Install docling-serve[ui] into /opt/zora/docling, for example with scripts/bootstrap-ubuntu.sh from a source checkout.
  2. Review /etc/zora/config.yaml.
  3. Enable services when ready:
       sudo systemctl enable --now docling-serve
       sudo systemctl enable --now zora
MSG
