#!/usr/bin/env bash
# Stop the user-level nginx instance and (optionally) the Go backend.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFIX="${AI_SCRATCH_NGINX_PREFIX:-$REPO_ROOT/deploy/nginx}"
NGINX_BIN="${NGINX_BIN:-/usr/sbin/nginx}"
[ -x "$NGINX_BIN" ] || NGINX_BIN="$(command -v nginx || true)"

if [ -f "$PREFIX/logs/nginx.pid" ] && kill -0 "$(cat "$PREFIX/logs/nginx.pid")" 2>/dev/null; then
  echo "[STOP] nginx"
  "$NGINX_BIN" -p "$PREFIX" -c "$PREFIX/nginx.conf" -s quit || true
else
  echo "[SKIP] nginx is not running"
fi

PID_FILE="${AI_SCRATCH_BACKEND_PID:-$REPO_ROOT/deploy/run/backend.pid}"
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "[STOP] backend pid $(cat "$PID_FILE")"
  kill "$(cat "$PID_FILE")" || true
  rm -f "$PID_FILE"
else
  echo "[SKIP] backend is not running"
fi
