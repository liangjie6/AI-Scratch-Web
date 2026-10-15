#!/usr/bin/env bash
# Install AI Scratch Web as a system service: system nginx site + backend unit.
# This is the only script in deploy/ that needs root.
#
#   sudo bash deploy/system/install-system.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_USER="${AI_SCRATCH_USER:-${SUDO_USER:-$(id -un)}}"
PORT="${AI_SCRATCH_PORT:-9966}"

if [ "$(id -u)" -ne 0 ]; then
  echo "[ERROR] This script must run as root: sudo bash $0" >&2
  exit 1
fi
if [ -e /etc/nginx/sites-enabled/ai-scratch-web-https.conf ]; then
  echo "[ERROR] The HTTPS site is already installed. Use publish-frontend.sh to update it." >&2
  exit 1
fi

echo "[1/4] Building the Go backend"
( cd "$REPO_ROOT/backend-go" && sudo -u "$RUN_USER" go build -o "$REPO_ROOT/backend-go/bin/ai-scratch-web-backend" ./cmd/server )

echo "[2/4] Installing the systemd unit"
sed -e "s|__REPO_ROOT__|$REPO_ROOT|g" -e "s|__RUN_USER__|$RUN_USER|g" \
    "$REPO_ROOT/deploy/system/ai-scratch-web-backend.service" \
    > /etc/systemd/system/ai-scratch-web-backend.service
systemctl daemon-reload
systemctl enable --now ai-scratch-web-backend.service

echo "[3/4] Installing the nginx site"
sed -e "s|__REPO_ROOT__|$REPO_ROOT|g" -e "s|9966|$PORT|g" \
    "$REPO_ROOT/deploy/system/ai-scratch-web.conf" \
    > /etc/nginx/sites-available/ai-scratch-web.conf
ln -sf /etc/nginx/sites-available/ai-scratch-web.conf /etc/nginx/sites-enabled/ai-scratch-web.conf
nginx -t
systemctl reload nginx

echo "[4/4] Stopping the user-level deployment so the port is free"
if [ -f "$REPO_ROOT/deploy/nginx/logs/nginx.pid" ]; then
  nginx -p "$REPO_ROOT/deploy/nginx" -c "$REPO_ROOT/deploy/nginx/nginx.conf" -s quit || true
fi
if [ -f "$REPO_ROOT/deploy/run/backend.pid" ]; then
  kill "$(cat "$REPO_ROOT/deploy/run/backend.pid")" 2>/dev/null || true
  rm -f "$REPO_ROOT/deploy/run/backend.pid"
fi

echo "[OK] system nginx + backend service installed on port $PORT"
echo "     Remember to open the firewall: sudo bash $REPO_ROOT/deploy/system/open-firewall.sh"
