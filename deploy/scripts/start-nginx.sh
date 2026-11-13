#!/usr/bin/env bash
# Start (or reload) the user-level nginx instance that serves AI Scratch Web.
#
# Everything the server needs — pid file, logs, temp paths — lives under
# deploy/nginx, so this works without root as long as the port is above 1024.
#
#   AI_SCRATCH_TLS=1 bash deploy/scripts/start-nginx.sh
# serves HTTPS on the same port using deploy/nginx/certs (see
# deploy/scripts/make-self-signed-cert.sh). HTTPS is the default for public
# access. AI_SCRATCH_TLS=0 binds only to loopback for local development.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFIX="${AI_SCRATCH_NGINX_PREFIX:-$REPO_ROOT/deploy/nginx}"
PORT="${AI_SCRATCH_PORT:-9966}"
BACKEND="${AI_SCRATCH_BACKEND:-127.0.0.1:8000}"
WEB_ROOT="${AI_SCRATCH_WEB_ROOT:-$REPO_ROOT/frontend/dist}"
TLS="${AI_SCRATCH_TLS:-1}"
CERT_DIR="${AI_SCRATCH_CERT_DIR:-$PREFIX/certs}"
NGINX_BIN="${NGINX_BIN:-/usr/sbin/nginx}"
CONFIG="$PREFIX/nginx.conf"
AUTH_FILE="${AI_SCRATCH_AUTH_FILE:-$PREFIX/private.htpasswd}"
if [ ! -r "$AUTH_FILE" ]; then
  echo "[ERROR] Create a private htpasswd file before starting nginx: $AUTH_FILE" >&2
  exit 1
fi
LISTEN_IPV4="127.0.0.1"
LISTEN_IPV6="[::1]"
if [ "$TLS" = "1" ]; then LISTEN_IPV4="0.0.0.0"; LISTEN_IPV6="[::]"; fi

if [ "$PORT" = "9966" ] && [ -e /etc/nginx/sites-enabled/ai-scratch-web-https.conf ] \
   && command -v systemctl > /dev/null 2>&1 && systemctl is-active --quiet nginx; then
  echo "[ERROR] Port 9966 is managed by system nginx. Use systemctl reload nginx." >&2
  exit 1
fi

if [ ! -x "$NGINX_BIN" ]; then
  NGINX_BIN="$(command -v nginx || true)"
fi
if [ -z "$NGINX_BIN" ]; then
  echo "[ERROR] nginx binary not found." >&2
  exit 1
fi
if [ ! -f "$WEB_ROOT/index.html" ]; then
  echo "[ERROR] Frontend bundle missing at $WEB_ROOT. Run: npm --prefix frontend run build" >&2
  exit 1
fi

mkdir -p "$PREFIX/logs" "$PREFIX/temp/client_body" "$PREFIX/temp/proxy" \
         "$PREFIX/temp/fastcgi" "$PREFIX/temp/uwsgi" "$PREFIX/temp/scgi"

TLS_LISTEN_SUFFIX=""
TLS_DIRECTIVES_FILE="$(mktemp)"
trap 'rm -f "$TLS_DIRECTIVES_FILE"' EXIT
if [ "$TLS" = "1" ]; then
  if [ ! -f "$CERT_DIR/ai-scratch-web.crt" ] || [ ! -f "$CERT_DIR/ai-scratch-web.key" ]; then
    echo "[ERROR] TLS requested but no certificate in $CERT_DIR." >&2
    echo "        Run: bash deploy/scripts/make-self-signed-cert.sh" >&2
    exit 1
  fi
  TLS_LISTEN_SUFFIX="ssl "
  {
    echo "        ssl_certificate     $CERT_DIR/ai-scratch-web.crt;"
    echo "        ssl_certificate_key $CERT_DIR/ai-scratch-web.key;"
    echo "        ssl_protocols       TLSv1.2 TLSv1.3;"
    echo "        ssl_ciphers         HIGH:!aNULL:!MD5;"
    echo "        ssl_session_cache   shared:ai_scratch_tls:2m;"
    echo "        ssl_session_timeout 1h;"
  } > "$TLS_DIRECTIVES_FILE"
fi

sed -e "s|__AUTH_FILE__|$AUTH_FILE|g" \
    -e "s|__LISTEN_IPV4__|$LISTEN_IPV4|g" \
    -e "s|__LISTEN_IPV6__|$LISTEN_IPV6|g" \
    -e "s|__PORT__|$PORT|g" \
    -e "s|__BACKEND__|$BACKEND|g" \
    -e "s|__WEB_ROOT__|$WEB_ROOT|g" \
    -e "s|__TLS_LISTEN_SUFFIX__|$TLS_LISTEN_SUFFIX|g" \
    -e "/__TLS_DIRECTIVES__/r $TLS_DIRECTIVES_FILE" \
    -e "/__TLS_DIRECTIVES__/d" \
    "$PREFIX/nginx.conf.template" > "$CONFIG"

SCHEME="http"
[ "$TLS" = "1" ] && SCHEME="https"

if [ -f "$PREFIX/logs/nginx.pid" ] && kill -0 "$(cat "$PREFIX/logs/nginx.pid")" 2>/dev/null; then
  echo "[RELOAD] nginx already running, reloading configuration"
  "$NGINX_BIN" -p "$PREFIX" -c "$CONFIG" -s reload
else
  echo "[START] nginx on $SCHEME port $PORT (IPv4 + IPv6)"
  "$NGINX_BIN" -p "$PREFIX" -c "$CONFIG"
fi
echo "[OK] $SCHEME://127.0.0.1:$PORT/  (LAN: $SCHEME://$(hostname -I 2>/dev/null | awk '{print $1}'):$PORT/)"
