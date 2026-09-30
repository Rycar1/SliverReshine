#!/usr/bin/env bash
# c2tool launcher — OPTIONAL, kept for compatibility.
#
# The binary is self-sufficient: it writes its own settings and login record on
# first start, generates the console password itself, and prints the URL. The
# release archive therefore ships the binary alone and does not include this
# script. It is kept so that a workflow already calling ./run.sh keeps working —
# everything here is now a flag override the binary would have applied itself.
#
# New deployments: just run ./c2tool.
#
# The console is password protected by default. An unauthenticated C2 console is
# both a liability and easy to catalog: asset engines and internet-wide scanners
# happily index any open port that answers HTTP.
set -euo pipefail

cd "$(dirname "$0")"

ADDR="${C2TOOL_ADDR:-0.0.0.0:8080}"
HOME_DIR="${C2TOOL_HOME:-$HOME/.c2tool}"
CRED_FILE="$HOME_DIR/console-auth"

BIN=./c2tool
# Zip archives written on Windows do not carry the unix executable bit, so a
# plain unzip leaves the launcher non-runnable. Restore it here rather than
# failing with a confusing "binary not found".
if [ -f "$BIN" ] && [ ! -x "$BIN" ]; then
  chmod +x "$BIN" 2>/dev/null || true
fi
if [ ! -x "$BIN" ]; then
  if [ -x ./c2tool.exe ]; then
    BIN=./c2tool.exe
  else
    echo "[c2tool] launcher binary not found next to $0" >&2
    exit 1
  fi
fi

# Generates a URL-safe random password without relying on a specific tool being
# installed: openssl when present, otherwise raw bytes from /dev/urandom.
gen_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 24 | tr -dc 'A-Za-z0-9' | cut -c1-24
  else
    # od is in coreutils and always available; hex is a safe alphabet.
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-24
  fi
}

AUTH_USER="${C2TOOL_AUTH_USER:-}"
AUTH_PASS="${C2TOOL_AUTH_PASS:-}"
AUTH_REALM="${C2TOOL_AUTH_REALM:-}"

if [ "${C2TOOL_AUTH:-on}" = "off" ]; then
  echo "[c2tool] WARNING: C2TOOL_AUTH=off — the console is open to anyone who can reach it." >&2
  AUTH_USER=""
  AUTH_PASS=""
else
  # The stored record is the console's single account, so read BOTH fields from
  # it. Taking only the password would let a restart revert a username that was
  # renamed in the Settings panel back to the default.
  if [ -z "$AUTH_USER" ] && [ -f "$CRED_FILE" ]; then
    AUTH_USER="$(cut -d: -f1 "$CRED_FILE")"
  fi
  if [ -z "$AUTH_USER" ]; then
    AUTH_USER="operator"
  fi
  if [ -z "$AUTH_PASS" ]; then
    if [ -f "$CRED_FILE" ]; then
      # Reuse the stored password so restarts do not invalidate bookmarks.
      AUTH_PASS="$(cut -d: -f2- "$CRED_FILE")"
    else
      AUTH_PASS="$(gen_password)"
      mkdir -p "$HOME_DIR"
      ( umask 077; printf '%s:%s\n' "$AUTH_USER" "$AUTH_PASS" > "$CRED_FILE" )
    fi
  fi
fi

echo "[c2tool] state:  $HOME_DIR"
echo "[c2tool] console will listen on http://$ADDR"
echo "[c2tool] first launch unpacks the implant toolchain and can take a minute"
echo

if [ -n "$AUTH_USER" ]; then
  echo "  ┌────────────────────────────────────────────────────────┐"
  echo "  │  HTTP Basic Auth is ON                                 │"
  echo "  └────────────────────────────────────────────────────────┘"
  echo "     username : $AUTH_USER"
  echo "     password : $AUTH_PASS"
  echo "     stored   : $CRED_FILE"
  echo
  echo "     The browser will prompt for these before serving anything."
  echo "     Change them by editing that file, or set C2TOOL_AUTH_USER"
  echo "     and C2TOOL_AUTH_PASS in the environment."
  echo
fi

if [ -n "$AUTH_USER" ]; then
  exec "$BIN" -addr "$ADDR" -auth-user "$AUTH_USER" -auth-pass "$AUTH_PASS" -auth-realm "$AUTH_REALM" "$@"
fi
exec "$BIN" -addr "$ADDR" "$@"
