#!/bin/bash
# End-to-end verification of a built Linux release archive.
#
# Usage:  verify-artifact.sh [path/to/c2tool-linux-amd64-*.zip]
#
# Extracts the archive, runs the console plus its embedded C2 server against an
# isolated home under /tmp, exercises the HTTP API with the generated Basic Auth
# credentials, then kills everything and removes the work directory.
#
# It exists because "the archive was produced" and "the archive works" are
# different claims, and the gap between them is where a packaging mistake hides:
# a wrong payload embedded, a missing exec bit, a route that never got registered.
# UPX-packed builds in particular need this -- packing rewrites the binary's
# loader and a broken result still unpacks and still has the right size.
#
# Nothing is left running and nothing outside /tmp is touched.
set -u

ZIP="${1:-$(dirname "$0")/../dist/c2tool-linux-amd64-plain.zip}"
if [ ! -f "$ZIP" ]; then
  echo "FAIL: archive not found: $ZIP" >&2
  exit 1
fi

# Fixed high ports, outside any range a host firewall or ephemeral allocator is
# likely to use for something else. Override if they collide.
CONSOLE_PORT="${CONSOLE_PORT:-17251}"
GRPC_PORT="${GRPC_PORT:-17252}"

WORK="$(mktemp -d /tmp/c2verify.XXXXXX)"
cleanup() {
  if [ -n "${CPID:-}" ]; then
    kill -TERM "$CPID" 2>/dev/null
    sleep 2
    kill -KILL "$CPID" 2>/dev/null
  fi
  cd /
  rm -rf "$WORK"
}
trap cleanup EXIT

cd "$WORK" || exit 1
unzip -q "$ZIP" || { echo "FAIL: unzip"; exit 1; }
chmod +x c2tool

echo "=== artifact ==="
file c2tool
ls -l c2tool

./c2tool -home "$WORK/data" \
  -addr "127.0.0.1:$CONSOLE_PORT" \
  -mp-host 127.0.0.1 -mp-port "$GRPC_PORT" \
  -operator verify > console.log 2>&1 &
CPID=$!
echo "console pid=$CPID"

for _ in $(seq 1 90); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$CONSOLE_PORT/" 2>/dev/null)
  if [ -n "$code" ] && [ "$code" != "000" ]; then break; fi
  sleep 2
done

echo "=== listening ports (console + embedded gRPC) ==="
(ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null) | grep -E "$CONSOLE_PORT|$GRPC_PORT" || echo "(none matched)"

# Unauthenticated access must be refused. This is the auth gate, not a failure.
echo "=== unauthenticated request must be 401 ==="
curl -s -o /dev/null -w 'status=%{http_code}\n' -m 5 "http://127.0.0.1:$CONSOLE_PORT/"

# The console writes "<user>:<pass>" to console-auth.
AUTH=""
for _ in $(seq 1 30); do
  if [ -s "$WORK/data/console-auth" ]; then
    AUTH=$(head -1 "$WORK/data/console-auth" | tr -d '\r\n')
    break
  fi
  sleep 2
done
if [ -z "$AUTH" ]; then echo "FAIL: no credentials written"; exit 1; fi

api() { curl -s -m 20 -u "$AUTH" "$@"; }

echo "=== GET /api/info ==="
api "http://127.0.0.1:$CONSOLE_PORT/api/info" | head -c 300; echo

echo "=== GET /api/jobs ==="
api "http://127.0.0.1:$CONSOLE_PORT/api/jobs" | head -c 200; echo

echo "=== GET /api/oneliner/targets ==="
api "http://127.0.0.1:$CONSOLE_PORT/api/oneliner/targets" | head -c 300; echo

# The multi-platform route must exist, must validate its input, and must report a
# per-platform error rather than failing the whole request. An unknown listener is
# the cheapest way to reach the build path without building anything.
echo "=== POST /api/oneliner/all, unknown listener: per-platform errors expected ==="
api -o all.json -w 'status=%{http_code}\n' \
  -X POST "http://127.0.0.1:$CONSOLE_PORT/api/oneliner/all" \
  -H 'Content-Type: application/json' \
  -d '{"job_id":9999,"platforms":["windows","linux"]}'
head -c 500 all.json; echo

echo "=== POST /api/oneliner/all, invalid platform: must be 400 before any build ==="
api -o bad.json -w 'status=%{http_code}\n' \
  -X POST "http://127.0.0.1:$CONSOLE_PORT/api/oneliner/all" \
  -H 'Content-Type: application/json' \
  -d '{"job_id":1,"platforms":["plan9"]}'
head -c 300 bad.json; echo

echo "=== POST /api/oneliner/all, missing job_id: must be 400 ==="
api -o nojob.json -w 'status=%{http_code}\n' \
  -X POST "http://127.0.0.1:$CONSOLE_PORT/api/oneliner/all" \
  -H 'Content-Type: application/json' \
  -d '{"platforms":["windows"]}'
head -c 300 nojob.json; echo

echo "=== console log tail ==="
tail -15 console.log

echo "=== shutting down ==="
kill -TERM "$CPID" 2>/dev/null
sleep 3
kill -KILL "$CPID" 2>/dev/null
CPID=""
sleep 1

echo "=== surviving c2tool processes ==="
# -x matches the process name exactly, not the whole command line. A -f match
# would also hit this script (its own arguments contain "c2tool") and report a
# leak that is really the check looking at itself.
pgrep -x c2tool || echo "(none - clean)"
echo "=== surviving sliver-server processes ==="
pgrep -x -f 'sliver-server-*' || echo "(none - clean)"
echo "=== done ==="
