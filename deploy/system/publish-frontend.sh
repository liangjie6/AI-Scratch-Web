#!/usr/bin/env bash
# Publish the built frontend to the directory served by system nginx.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SOURCE="$REPO_ROOT/frontend/dist"
TARGET="/var/www/ai-scratch-web"

if [ "$(id -u)" -ne 0 ]; then
  echo "[ERROR] Run as root: sudo bash $0" >&2
  exit 1
fi
if [ ! -f "$SOURCE/index.html" ]; then
  echo "[ERROR] Frontend bundle missing at $SOURCE. Run: npm --prefix frontend run build" >&2
  exit 1
fi

install -d -o root -g root -m 755 "$TARGET"
rsync -a --delete --chown=root:root --chmod=D755,F644 "$SOURCE/" "$TARGET/"
echo "[OK] Published $SOURCE to $TARGET"
