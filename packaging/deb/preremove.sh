#!/bin/sh
set -e

if [ "$1" = "remove" ] && command -v systemctl >/dev/null 2>&1; then
	systemctl stop zora >/dev/null 2>&1 || true
	systemctl stop docling-serve >/dev/null 2>&1 || true
fi
