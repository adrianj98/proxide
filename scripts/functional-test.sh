#!/usr/bin/env bash
# End-to-end functional test for devproxy.
#
# Spins up a pseudo target service (HTTP/SSE/WebSocket), the edge, and the agent,
# then drives traffic through the public port with curl (plus a WebSocket probe)
# and asserts the responses. Also checks token auth and agent reconnect.
#
# Usage: scripts/functional-test.sh
# Tunable via env: CTRL_PORT, PUB_PORT, TGT_PORT, TOKEN, HOST
set -euo pipefail

HOST="${HOST:-127.0.0.1}"
CTRL_PORT="${CTRL_PORT:-17223}"
PUB_PORT="${PUB_PORT:-18080}"
TGT_PORT="${TGT_PORT:-19000}"
ADMIN_PORT="${ADMIN_PORT:-19443}"
TOKEN="${TOKEN:-test-secret}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PUB="http://$HOST:$PUB_PORT"
WS="ws://$HOST:$PUB_PORT"
CTRL="http://$HOST:$CTRL_PORT/tunnel"

pids=()
EDGE_PID=""
cleanup() {
	[ -n "$EDGE_PID" ] && kill "$EDGE_PID" 2>/dev/null || true
	for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
	wait 2>/dev/null || true
}
trap cleanup EXIT

dump_logs() {
	echo "----- target log -----"; cat /tmp/ft-target.log 2>/dev/null || true
	echo "----- edge log -----"; cat /tmp/ft-edge.log 2>/dev/null || true
	echo "----- agent log -----"; cat /tmp/ft-agent.log 2>/dev/null || true
}
fail() { echo "FAIL: $1" >&2; dump_logs; exit 1; }
pass() { echo "PASS: $1"; }

start_edge() {
	./bin/edge --control-addr "$HOST:$CTRL_PORT" --public-addr "$HOST:$PUB_PORT" \
		--admin-addr "$HOST:$ADMIN_PORT" --token "$TOKEN" >>/tmp/ft-edge.log 2>&1 &
	EDGE_PID=$!
}

wait_up() {
	for _ in $(seq 1 50); do
		if curl -fsS --max-time 2 "$PUB/" >/dev/null 2>&1; then return 0; fi
		sleep 0.2
	done
	return 1
}

echo ">> building binaries"
go build -o bin/edge ./cmd/edge
go build -o bin/agent ./cmd/agent
go build -o bin/testserver ./cmd/_testserver/main.go
go build -o bin/wsprobe ./cmd/_wsprobe/main.go
: >/tmp/ft-edge.log

echo ">> starting pseudo target service on $HOST:$TGT_PORT"
./bin/testserver -addr "$HOST:$TGT_PORT" >/tmp/ft-target.log 2>&1 &
pids+=($!)

echo ">> starting edge ($HOST:$CTRL_PORT control, $HOST:$PUB_PORT public)"
start_edge

echo ">> starting agent -> $HOST:$TGT_PORT"
./bin/agent --edge-url "ws://$HOST:$CTRL_PORT/tunnel" --target "$HOST:$TGT_PORT" \
	--token "$TOKEN" >/tmp/ft-agent.log 2>&1 &
pids+=($!)

echo ">> waiting for tunnel"
wait_up || fail "tunnel did not come up"
pass "tunnel established"

# 1. HTTP/1.1
body="$(curl -fsS --max-time 5 "$PUB/hello")"
echo "$body" | grep -q "hello from target service" || fail "HTTP body unexpected: $body"
pass "HTTP/1.1 request"

# 2. Concurrency (20 parallel requests)
cpids=()
for i in $(seq 1 20); do
	curl -fsS --max-time 5 "$PUB/c$i" >/dev/null 2>&1 &
	cpids+=($!)
done
ok=0
for pid in "${cpids[@]}"; do
	if wait "$pid"; then ok=$((ok + 1)); fi
done
[ "$ok" -eq 20 ] || fail "concurrency: only $ok/20 succeeded"
pass "20 concurrent requests"

# 3. SSE streaming
sse="$(curl -fsS -N --max-time 5 "$PUB/sse")"
echo "$sse" | grep -q "tick 0" || fail "SSE missing first event: $sse"
echo "$sse" | grep -q "tick 4" || fail "SSE missing last event: $sse"
pass "SSE streaming"

# 4. WebSocket echo
./bin/wsprobe -url "$WS/ws" || fail "WebSocket echo"
pass "WebSocket echo"

# 5. Token auth on the control plane
code="$(curl -s -o /dev/null -w '%{http_code}' "$CTRL")"
[ "$code" = "401" ] || fail "no-token control request: expected 401, got $code"
code="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer WRONG" "$CTRL")"
[ "$code" = "401" ] || fail "wrong-token control request: expected 401, got $code"
code="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$CTRL")"
[ "$code" = "426" ] || fail "valid-token control request: expected 426, got $code"
pass "token auth (401 reject, 426 upgrade-required)"

# 6. Reconnect after edge restart
kill "$EDGE_PID" 2>/dev/null || true
wait "$EDGE_PID" 2>/dev/null || true
EDGE_PID=""
# brief window where the public port is down
curl -fsS --max-time 2 "$PUB/while-down" >/dev/null 2>&1 && fail "request unexpectedly succeeded while edge down"
start_edge
wait_up || fail "tunnel did not recover after edge restart"
curl -fsS --max-time 5 "$PUB/after-restart" >/dev/null 2>&1 || fail "request failed after recovery"
pass "agent reconnect after edge restart"

# 7. Admin console: login + run a command inside the container
ADMIN="http://$HOST:$ADMIN_PORT"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST --data 'token=WRONG' "$ADMIN/login")"
[ "$code" = "401" ] || fail "admin login wrong token: expected 401, got $code"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST --data 'echo no' "$ADMIN/exec")"
[ "$code" = "401" ] || fail "admin exec without session: expected 401, got $code"
curl -s -o /dev/null -X POST --data "token=$TOKEN" "$ADMIN/login" -c /tmp/ft-cookies.txt
exec_out="$(curl -s -X POST -H 'Accept: text/plain' --data 'echo DEVPROXY_EXEC_OK' \
	-b /tmp/ft-cookies.txt "$ADMIN/exec")"
echo "$exec_out" | grep -q "DEVPROXY_EXEC_OK" || fail "admin exec output unexpected: $exec_out"
pass "admin console login + command exec"

echo "ALL FUNCTIONAL TESTS PASSED"
