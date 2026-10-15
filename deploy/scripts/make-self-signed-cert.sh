#!/usr/bin/env bash
# Generate a self-signed TLS certificate for the nginx deployment.
#
#   bash deploy/scripts/make-self-signed-cert.sh
#
# The certificate carries subjectAltName entries for localhost, the loopback
# addresses, the primary LAN IPv4 address and every global IPv6 address, so
# https://<lan-ip>:9966/ and https://[<global-v6>]:9966/ both validate after the
# browser exception is accepted.
#
# Serving over HTTPS makes the page a secure context, which is the alternative
# to the built-in SHA-256 fallback for browsers that need crypto.subtle.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CERT_DIR="${AI_SCRATCH_CERT_DIR:-$REPO_ROOT/deploy/nginx/certs}"
DAYS="${AI_SCRATCH_CERT_DAYS:-825}"

if ! command -v openssl > /dev/null 2>&1; then
  echo "[ERROR] openssl is required." >&2
  exit 1
fi

mkdir -p "$CERT_DIR"

SANS="DNS:localhost,IP:127.0.0.1,IP:::1"
for address in $(hostname -I 2>/dev/null || true); do
  case "$address" in
    *:*) SANS="$SANS,IP:$address" ;;
    *)   SANS="$SANS,IP:$address" ;;
  esac
done
# hostname -I does not always list the stable global IPv6 address.
for address in $(ip -6 addr show scope global 2>/dev/null | awk '/inet6/ {print $2}' | cut -d/ -f1 || true); do
  SANS="$SANS,IP:$address"
done

echo "[CERT] subjectAltName: $SANS"
openssl req -x509 -nodes -newkey rsa:2048 -days "$DAYS" \
  -keyout "$CERT_DIR/ai-scratch-web.key" \
  -out "$CERT_DIR/ai-scratch-web.crt" \
  -subj "/CN=ai-scratch-web" \
  -addext "subjectAltName=$SANS" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth"

chmod 600 "$CERT_DIR/ai-scratch-web.key"
chmod 644 "$CERT_DIR/ai-scratch-web.crt"
echo "[OK] certificate written to $CERT_DIR"
echo "     Start the TLS deployment with: AI_SCRATCH_TLS=1 bash deploy/scripts/start-nginx.sh"
