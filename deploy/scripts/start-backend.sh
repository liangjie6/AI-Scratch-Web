#!/usr/bin/env bash
# Build and start the Go backend on 127.0.0.1:8000 behind nginx.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_DIR="${AI_SCRATCH_RUN_DIR:-$REPO_ROOT/deploy/run}"
BIN="$REPO_ROOT/backend-go/bin/ai-scratch-web-backend"
HOST="${AI_SCRATCH_HOST:-127.0.0.1}"
PORT="${AI_SCRATCH_PORT_BACKEND:-8000}"
PID_FILE="$RUN_DIR/backend.pid"

if command -v systemctl > /dev/null 2>&1 && systemctl is-active --quiet ai-scratch-web-backend.service; then
  echo "[ERROR] The backend is managed by systemd. Use systemctl restart ai-scratch-web-backend.service." >&2
  exit 1
fi

mkdir -p "$RUN_DIR" "$REPO_ROOT/backend-go/bin"

echo "[BUILD] Go backend"
( cd "$REPO_ROOT/backend-go" && go build -o "$BIN" ./cmd/server )

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "[STOP] Replacing running backend pid $(cat "$PID_FILE")"
  kill "$(cat "$PID_FILE")" || true
  for _ in $(seq 1 50); do
    kill -0 "$(cat "$PID_FILE")" 2>/dev/null || break
    sleep 0.1
  done
  rm -f "$PID_FILE"
fi

# The backend reads .env from its working directory.
cd "$REPO_ROOT/backend-go"
if [ -n "${HTTPS_PROXY:-}" ]; then
  echo "[INFO] Using HTTPS_PROXY=$HTTPS_PROXY"
fi
# setsid detaches the server into its own session so it survives the shell
# that launched it (no systemd unit is required for the user-level deployment).
setsid nohup "$BIN" --host "$HOST" --port "$PORT" < /dev/null > "$RUN_DIR/backend.log" 2>&1 &
echo $! > "$PID_FILE"
echo "[START] backend pid $(cat "$PID_FILE") on http://$HOST:$PORT"

for _ in $(seq 1 50); do
  if curl -fsS "http://$HOST:$PORT/health" > /dev/null 2>&1; then
    echo "[READY] backend health check passed"
    exit 0
  fi
  sleep 0.2
done
echo "[WARN] backend did not answer /health yet; see $RUN_DIR/backend.log" >&2
