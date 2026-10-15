#!/usr/bin/env bash
# Open TCP 9966 for both IPv4 and IPv6. Requires root.
#
#   sudo bash deploy/system/open-firewall.sh
#
# The rule is added even when ufw is currently inactive, because
# /etc/ufw/ufw.conf has ENABLED=yes on this host: the ruleset is applied again
# after a reboot or after `sudo ufw enable`, and the port would otherwise be
# blocked.
set -euo pipefail

PORT="${AI_SCRATCH_PORT:-9966}"

if [ "$(id -u)" -ne 0 ]; then
  echo "[ERROR] This script must run as root: sudo bash $0" >&2
  exit 1
fi

if command -v ufw > /dev/null 2>&1; then
  echo "[FIREWALL] ufw"
  if ufw status 2>/dev/null | grep -qi '^Status: active'; then
    echo "  state: active"
  else
    echo "  state: inactive (rules are still persisted and take effect when ufw starts)"
  fi
  ufw allow "$PORT/tcp" comment 'AI Scratch Web'
  if grep -q '^IPV6=yes' /etc/default/ufw 2>/dev/null; then
    echo "  IPv6 support: enabled (ufw manages ip6tables too)"
  else
    echo "  [WARN] IPV6 is not 'yes' in /etc/default/ufw; adding an explicit ip6tables rule"
    ip6tables -C INPUT -p tcp --dport "$PORT" -j ACCEPT 2>/dev/null || ip6tables -I INPUT -p tcp --dport "$PORT" -j ACCEPT
  fi
  ufw status verbose || true
elif command -v firewall-cmd > /dev/null 2>&1 && firewall-cmd --state > /dev/null 2>&1; then
  echo "[FIREWALL] firewalld"
  firewall-cmd --permanent --add-port="$PORT/tcp"
  firewall-cmd --reload
  firewall-cmd --list-ports
else
  echo "[FIREWALL] no ufw/firewalld detected; installing explicit nftables/iptables rules"
  if command -v nft > /dev/null 2>&1; then
    nft add table inet ai_scratch_web 2>/dev/null || true
    nft add chain inet ai_scratch_web input '{ type filter hook input priority 0 ; policy accept ; }' 2>/dev/null || true
    nft add rule inet ai_scratch_web input tcp dport "$PORT" accept 2>/dev/null || true
    nft list table inet ai_scratch_web
  else
    iptables -C INPUT -p tcp --dport "$PORT" -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport "$PORT" -j ACCEPT
    ip6tables -C INPUT -p tcp --dport "$PORT" -j ACCEPT 2>/dev/null || ip6tables -I INPUT -p tcp --dport "$PORT" -j ACCEPT
    iptables -S INPUT | grep "$PORT" || true
    ip6tables -S INPUT | grep "$PORT" || true
  fi
fi

echo "[OK] TCP $PORT is open for IPv4 and IPv6"
