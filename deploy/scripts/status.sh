#!/usr/bin/env bash
# Report the state of the AI Scratch Web deployment.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFIX="${AI_SCRATCH_NGINX_PREFIX:-$REPO_ROOT/deploy/nginx}"
PORT="${AI_SCRATCH_PORT:-9966}"
BACKEND_PORT="${AI_SCRATCH_PORT_BACKEND:-8000}"
RUN_DIR="${AI_SCRATCH_RUN_DIR:-$REPO_ROOT/deploy/run}"
SCHEME="http"
HTTPS_STATUS="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 5 "https://127.0.0.1:$PORT/" 2>/dev/null)"
if [[ "$HTTPS_STATUS" =~ ^[1-5][0-9][0-9]$ ]]; then
  SCHEME="https"
fi

echo "== ports =="
ss -tlnp 2>/dev/null | grep -E ":$PORT|:$BACKEND_PORT" || echo "  nothing listening on $PORT / $BACKEND_PORT"

echo "== backend =="
if curl -fsS "http://127.0.0.1:$BACKEND_PORT/healthz" 2>/dev/null; then
  echo
else
  echo "  backend is not answering http://127.0.0.1:$BACKEND_PORT/healthz"
fi

echo "== nginx =="
for url in "$SCHEME://127.0.0.1:$PORT/" "$SCHEME://[::1]:$PORT/" "$SCHEME://127.0.0.1:$PORT/healthz"; do
  printf '  %-44s %s\n' "$url" "$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null)"
done

echo "== firewall =="
if command -v ufw > /dev/null 2>&1; then
  if ufw status 2>/dev/null | head -1 > /dev/null 2>&1; then
    ufw status | head -20
  else
    grep -E '^ENABLED' /etc/ufw/ufw.conf 2>/dev/null | sed 's/^/  ufw.conf: /'
    echo "  (run 'sudo ufw status' for live rules; this shell has no root)"
  fi
fi

echo "== logs =="
if [ -e /etc/nginx/sites-enabled/ai-scratch-web-https.conf ] && systemctl is-active --quiet nginx 2>/dev/null; then
  echo "  nginx:   /var/log/nginx/error.log"
else
  echo "  nginx:   $PREFIX/logs/error.log"
fi
if systemctl is-active --quiet ai-scratch-web-backend.service 2>/dev/null; then
  echo "  backend: journalctl -u ai-scratch-web-backend.service"
else
  echo "  backend: $RUN_DIR/backend.log"
fi
