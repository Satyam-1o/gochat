#!/usr/bin/env bash
#
# End-to-end smoke test for gochat using the real binaries:
#   1. build the client and rendezvous node
#   2. start the rendezvous node on a private port
#   3. start two headless clients that discover each other through it
#   4. assert that an encrypted message is delivered
#
# Usage: ./scripts/smoke-test.sh
# Environment overrides: PORT, PUBLIC, TIMEOUT.

set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-7401}"
PUBLIC="${PUBLIC:-127.0.0.1}"
TIMEOUT="${TIMEOUT:-45}"

BIN="./bin"
TMP="$(mktemp -d)"
RELAY_PID=""
ALICE_PID=""

cleanup() {
  [ -n "$ALICE_PID" ] && kill "$ALICE_PID" 2>/dev/null || true
  [ -n "$RELAY_PID" ] && kill "$RELAY_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

say "Building binaries"
go build -o "$BIN/gochat" ./cmd/gochat
go build -o "$BIN/gochat-rendezvous" ./cmd/gochat-rendezvous

say "Starting rendezvous node on port $PORT"
"$BIN/gochat-rendezvous" -port "$PORT" -data "$TMP/relay" -public "$PUBLIC" \
  >"$TMP/relay.log" 2>&1 &
RELAY_PID=$!

# Wait for the node to print a client-ready bootstrap address.
ADDR=""
for _ in $(seq 1 100); do
  if grep -q "client: gochat -bootstrap" "$TMP/relay.log" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$RELAY_PID" 2>/dev/null; then
    echo "--- relay log ---"; cat "$TMP/relay.log"; fail "rendezvous node exited early"
  fi
  sleep 0.1
done

ADDR="$(grep -oE 'client: gochat -bootstrap "[^"]+"' "$TMP/relay.log" | head -n1 | sed -E 's/.*"(.*)"/\1/')"
[ -n "$ADDR" ] || { echo "--- relay log ---"; cat "$TMP/relay.log"; fail "could not parse bootstrap address"; }
say "Bootstrap address: $ADDR"

ROOM="smoke-$RANDOM-$$"
PASS="s3cr3t-$RANDOM-$$"
MSG="hello-from-alice-$RANDOM-$$"

say "Starting receiver (bob) waiting for: $MSG"
"$BIN/gochat" -headless -config "$TMP/bob" -nick bob \
  -bootstrap "$ADDR" -room "$ROOM" -pass "$PASS" \
  -expect "$MSG" -timeout "${TIMEOUT}s" >"$TMP/bob.log" 2>&1 &
BOB_PID=$!

# Give bob a head start so both are subscribed when alice publishes.
sleep 2

say "Starting sender (alice)"
"$BIN/gochat" -headless -config "$TMP/alice" -nick alice \
  -bootstrap "$ADDR" -room "$ROOM" -pass "$PASS" \
  -send "$MSG" -timeout "$((TIMEOUT - 5))s" >"$TMP/alice.log" 2>&1 &
ALICE_PID=$!

if wait "$BOB_PID"; then
  say "Message delivered — smoke test PASSED"
  echo "--- receiver ---"; cat "$TMP/bob.log"
  echo "--- sender ---";   cat "$TMP/alice.log"
  exit 0
fi

echo "--- relay ---"   >&2; cat "$TMP/relay.log"   >&2
echo "--- receiver ---" >&2; cat "$TMP/bob.log"   >&2
echo "--- sender ---"   >&2; cat "$TMP/alice.log" >&2
fail "message was not delivered"
