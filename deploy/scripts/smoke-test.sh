#!/usr/bin/env bash
# End-to-end smoke test for a running AI Scratch Web deployment.
#
#   bash deploy/scripts/smoke-test.sh [base-url]
#
# Defaults to the nginx front door on port 9966. Pass http://127.0.0.1:8000 to
# test the backend directly.
set -uo pipefail

BASE="${1:-${AI_SCRATCH_BASE_URL:-https://127.0.0.1:9966}}"
PASS=0
FAIL=0

check() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    PASS=$((PASS + 1))
    printf '  ok   %-46s %s\n' "$label" "$actual"
  else
    FAIL=$((FAIL + 1))
    printf '  FAIL %-46s expected %s got %s\n' "$label" "$expected" "$actual"
  fi
}

CURL_AUTH=()
if [ -n "${AI_SCRATCH_NETRC_FILE:-}" ]; then
  CURL_AUTH=(--netrc-file "$AI_SCRATCH_NETRC_FILE")
fi
status() { curl -s -o /dev/null -w '%{http_code}' --max-time 20 "${CURL_AUTH[@]}" "$@"; }
if [ -z "${AI_SCRATCH_NETRC_FILE:-}" ]; then
  echo "== anonymous private-access boundary only =="
  check "GET / requires login" 401 "$(status "$BASE/")"
  check "GET /api/capabilities requires login" 401 "$(status "$BASE/api/capabilities")"
  check "POST /api/image/generate requires login" 401 "$(status -X POST -d '{}' "$BASE/api/image/generate")"
  echo "For functional checks, provide a chmod-600 AI_SCRATCH_NETRC_FILE."
  [ "$FAIL" -eq 0 ]; exit $?
fi

echo "== static frontend =="
check "GET /" 200 "$(status "$BASE/")"
check "GET /index.html" 200 "$(status "$BASE/index.html")"
check "GET /assets/scratch/editor-capabilities.json" 200 "$(status "$BASE/assets/scratch/editor-capabilities.json")"
check "SPA fallback /deep/route" 200 "$(status "$BASE/deep/route")"

CHUNK="$(ls "$(dirname "${BASH_SOURCE[0]}")/../../frontend/dist/assets/scratch/chunks" 2>/dev/null | head -1)"
if [ -n "$CHUNK" ]; then
  check "GET /chunks/$CHUNK (webpack public path rewrite)" 200 "$(status "$BASE/chunks/$CHUNK")"
fi

echo "== api through the front door =="
check "GET /health" 200 "$(status "$BASE/health")"
check "GET /api/capabilities" 200 "$(status "$BASE/api/capabilities")"
check "POST /api/image/generate (invalid body -> 422)" 422 \
  "$(status -X POST -H 'Content-Type: application/json' -d '{"prompt":"x","style":"nope","width":1,"height":1,"target":"sprite","sheet_layout":"1x1"}' "$BASE/api/image/generate")"
check "POST /api/ai-blocks/validate (empty body -> 422)" 422 \
  "$(status -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/ai-blocks/validate")"
RUNS_STATUS="$(status -H 'X-AI-Run-Scope: smoke-scope' "$BASE/api/ai-runs/metrics")"
if [ "$RUNS_STATUS" = "200" ] || [ "$RUNS_STATUS" = "404" ]; then
  PASS=$((PASS + 1))
  printf '  ok   %-46s %s\n' "GET /api/ai-runs/metrics (enabled or gated off)" "$RUNS_STATUS"
else
  FAIL=$((FAIL + 1))
  printf '  FAIL %-46s unexpected %s\n' "GET /api/ai-runs/metrics" "$RUNS_STATUS"
fi

echo "== manifest validation round trip =="
VALIDATION="$(curl -s "${CURL_AUTH[@]}" --max-time 20 -X POST -H 'Content-Type: application/json' \
  -d '{"kind":"program","payload":{"schema_version":1,"summary":"smoke","scripts":[{"id":"script_smoke","purpose":"smoke","x":0,"y":0,"blocks":[{"opcode":"event_whenflagclicked"}]}]},"target_kind":"sprite","mode":"append"}' \
  "$BASE/api/ai-blocks/validate")"
case "$VALIDATION" in
  *'"valid":true'*) PASS=$((PASS + 1)); echo "  ok   /api/ai-blocks/validate accepts a minimal ProgramIR" ;;
  *) FAIL=$((FAIL + 1)); echo "  FAIL /api/ai-blocks/validate rejected a minimal ProgramIR: $VALIDATION" ;;
esac

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
