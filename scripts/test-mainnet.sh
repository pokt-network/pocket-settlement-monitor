#!/usr/bin/env bash
#
# Mainnet Integration Test Suite for pocket-settlement-monitor
#
# Usage: ./scripts/test-mainnet.sh
#
# Prerequisites:
#   - Binary built: make build
#   - Mainnet RPC reachable: https://sauron-rpc.infra.pocket.network
#
# Mainnet parameters:
#   - Block time: ~1.5 minutes
#   - Settlement period: every 60 blocks (~90 minutes)
#   - Last known settlement: block 646233
#   - Next settlement: block 646293
#
set -euo pipefail

BINARY="./bin/pocket-settlement-monitor"
CONFIG="config.mainnet.yaml"
DB="./settlement-monitor-mainnet.db"
RPC="https://sauron-rpc.infra.pocket.network"
SETTLEMENT_BLOCK=646233
METRICS_PORT=9091

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
SKIP=0

check() {
    local name="$1" got="$2" expected="$3"
    if [ "$got" -eq "$expected" ]; then
        echo -e "  ${GREEN}PASS${NC}: $name (exit $got)"
        PASS=$((PASS+1))
    else
        echo -e "  ${RED}FAIL${NC}: $name (exit $got, expected $expected)"
        FAIL=$((FAIL+1))
    fi
}

skip() {
    local name="$1" reason="$2"
    echo -e "  ${YELLOW}SKIP${NC}: $name ($reason)"
    SKIP=$((SKIP+1))
}

cleanup() {
    rm -f "$DB" "${DB}-wal" "${DB}-shm"
    # Kill any leftover monitor processes
    pkill -f "pocket-settlement-monitor.*mainnet" 2>/dev/null || true
}

# ============================================================
echo "=========================================="
echo "MAINNET INTEGRATION TEST SUITE"
echo "=========================================="
echo ""

# ============================================================
echo "--- Phase 0: Prerequisites ---"
echo ""

# Check binary exists
if [ ! -x "$BINARY" ]; then
    echo -e "${RED}ERROR: Binary not found. Run 'make build' first.${NC}"
    exit 1
fi
echo "  Binary: $($BINARY version 2>&1 | grep -v maxprocs)"

# Check config exists
if [ ! -f "$CONFIG" ]; then
    echo -e "${RED}ERROR: Config not found: $CONFIG${NC}"
    exit 1
fi
echo "  Config: $CONFIG"

# Check mainnet RPC connectivity
echo -n "  RPC connectivity: "
STATUS=$(curl -s --max-time 15 "${RPC}/status" 2>/dev/null)
if [ -z "$STATUS" ]; then
    echo -e "${RED}FAILED — cannot reach $RPC${NC}"
    exit 1
fi
NETWORK=$(echo "$STATUS" | python3 -c "import json,sys; print(json.load(sys.stdin)['result']['node_info']['network'])" 2>/dev/null)
LATEST=$(echo "$STATUS" | python3 -c "import json,sys; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null)
echo -e "${GREEN}OK${NC} (network: $NETWORK, height: $LATEST)"

# Calculate next settlement
NEXT_SETTLEMENT=$SETTLEMENT_BLOCK
while [ "$NEXT_SETTLEMENT" -le "$LATEST" ]; do
    NEXT_SETTLEMENT=$((NEXT_SETTLEMENT + 60))
done
BLOCKS_AWAY=$((NEXT_SETTLEMENT - LATEST))
MINS_AWAY=$(echo "$BLOCKS_AWAY * 1.5" | bc 2>/dev/null || echo "$((BLOCKS_AWAY * 3 / 2))")
echo "  Next settlement: block $NEXT_SETTLEMENT (~${BLOCKS_AWAY} blocks, ~${MINS_AWAY} min away)"
echo ""

# Clean up before tests
cleanup

# ============================================================
echo "--- Phase 1: RPC Validation ---"
echo ""

# Verify a non-settlement block is reachable (small response, fast)
echo -n "  Checking RPC block_results (non-settlement block): "
BLOCK_DATA=$(curl -s --max-time 15 "${RPC}/block_results?height=$((SETTLEMENT_BLOCK+1))" 2>/dev/null)
if [ -z "$BLOCK_DATA" ]; then
    echo -e "${RED}FAILED — no response${NC}"
    FAIL=$((FAIL+1))
else
    echo -e "${GREEN}OK${NC}"
    PASS=$((PASS+1))
fi

# NOTE: Settlement blocks on mainnet have 1GB+ block_results responses.
# We skip curl-based validation and test via the tool's backfill command instead,
# which uses Go's HTTP client with proper streaming.
echo "  (Settlement block validation done via backfill in Phase 2 — mainnet blocks are 1GB+)"
echo ""

# ============================================================
echo "--- Phase 2: Backfill Against Known Settlement Block ---"
echo ""

# Backfill single settlement block
echo "  Backfilling block $SETTLEMENT_BLOCK (mainnet blocks are large, may take 30-60s)..."
BACKFILL_OUTPUT=$($BINARY backfill --config "$CONFIG" --from "$SETTLEMENT_BLOCK" --to "$SETTLEMENT_BLOCK" 2>&1)
BACKFILL_EXIT=$?
check "backfill settlement block" $BACKFILL_EXIT 0

# Verify events were persisted — match either structured log or printf output
if [ $BACKFILL_EXIT -eq 0 ]; then
    # Try structured log format: events_found=N
    EVENTS_FOUND=$(echo "$BACKFILL_OUTPUT" | grep -oP 'events_found=\K\d+' 2>/dev/null || true)
    # Try printf format: N events found
    if [ -z "$EVENTS_FOUND" ]; then
        EVENTS_FOUND=$(echo "$BACKFILL_OUTPUT" | grep -oP '\d+(?= events found)' 2>/dev/null || true)
    fi
    EVENTS_FOUND=${EVENTS_FOUND:-0}
    echo "  Events found: $EVENTS_FOUND"
    if [ "$EVENTS_FOUND" -gt 0 ]; then
        check "events persisted ($EVENTS_FOUND)" 0 0
    else
        check "events persisted (none found)" 1 0
    fi
fi

# Backfill a small range around the settlement block (mainnet blocks are heavy)
echo ""
echo "  Backfilling range $((SETTLEMENT_BLOCK-1)) to $((SETTLEMENT_BLOCK+1))..."
$BINARY backfill --config "$CONFIG" --from "$((SETTLEMENT_BLOCK-1))" --to "$((SETTLEMENT_BLOCK+1))" >/dev/null 2>&1
check "backfill range" $? 0

echo ""

# ============================================================
echo "--- Phase 3: Query Validation ---"
echo ""

# Query settlements
QUERY_OUTPUT=$($BINARY query settlements --config "$CONFIG" --output json --limit 5 2>&1)
check "query settlements" $? 0

# Verify query returns data
SETTLEMENT_COUNT=$(echo "$QUERY_OUTPUT" | python3 -c "
import json, sys
lines = sys.stdin.read().strip().split('\n')
for line in lines:
    try:
        data = json.loads(line)
        if isinstance(data, list):
            print(len(data))
            break
    except: pass
else:
    print(0)
" 2>/dev/null)
if [ "${SETTLEMENT_COUNT:-0}" -gt 0 ]; then
    check "settlements returned ($SETTLEMENT_COUNT)" 0 0
else
    check "settlements returned (empty)" 1 0
fi

# Query summaries
$BINARY query summaries --config "$CONFIG" >/dev/null 2>&1
check "query summaries" $? 0

# Query reimbursements
$BINARY query reimbursements --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query reimbursements" $? 0

# Query overservice
$BINARY query overservice --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query overservice" $? 0

# Query slashes
$BINARY query slashes --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query slashes" $? 0

# CSV output
$BINARY query settlements --config "$CONFIG" --output csv --limit 3 >/dev/null 2>&1
check "query csv output" $? 0

# JSON output
$BINARY query settlements --config "$CONFIG" --output json --limit 3 >/dev/null 2>&1
check "query json output" $? 0

echo ""

# ============================================================
echo "--- Phase 4: Live Monitor (WebSocket) ---"
echo ""

# Start monitor, verify it connects
echo "  Starting monitor (15s test)..."
$BINARY monitor --config "$CONFIG" 2>&1 &
MON_PID=$!
sleep 10

# Check health endpoint
HEALTH=$(curl -s --max-time 5 "http://localhost:${METRICS_PORT}/health" 2>/dev/null)
if echo "$HEALTH" | grep -q '"ok"'; then
    check "health endpoint" 0 0
else
    check "health endpoint" 1 0
fi

# Check ready endpoint
READY=$(curl -s --max-time 5 "http://localhost:${METRICS_PORT}/ready" 2>/dev/null)
if echo "$READY" | grep -q '"ok"'; then
    check "ready endpoint" 0 0
else
    check "ready endpoint" 1 0
fi

# Check metrics endpoint
METRICS=$(curl -s --max-time 5 "http://localhost:${METRICS_PORT}/metrics" 2>/dev/null)
if echo "$METRICS" | grep -q "psm_websocket_connected 1"; then
    check "websocket connected metric" 0 0
else
    check "websocket connected metric" 1 0
fi

if echo "$METRICS" | grep -q "psm_info"; then
    check "info metric present" 0 0
else
    check "info metric present" 1 0
fi

# Stop monitor gracefully
kill -TERM $MON_PID 2>/dev/null
wait $MON_PID 2>/dev/null
check "monitor SIGTERM exit" $? 0

echo ""

# ============================================================
echo "--- Phase 5: Signal Handling ---"
echo ""

# SIGINT on monitor
$BINARY monitor --config "$CONFIG" 2>/dev/null &
PID=$!; sleep 5; kill -INT $PID; wait $PID 2>/dev/null
check "monitor SIGINT" $? 0

# SIGTERM during backfill (use non-settlement range for fast responses)
$BINARY backfill --config "$CONFIG" --from "$((SETTLEMENT_BLOCK+1))" --to "$((SETTLEMENT_BLOCK+20))" --delay 1s 2>/dev/null &
PID=$!; sleep 8; kill -TERM $PID; wait $PID 2>/dev/null
check "backfill SIGTERM" $? 0

# SIGINT during backfill
$BINARY backfill --config "$CONFIG" --from "$((SETTLEMENT_BLOCK+1))" --to "$((SETTLEMENT_BLOCK+20))" --delay 1s 2>/dev/null &
PID=$!; sleep 8; kill -INT $PID; wait $PID 2>/dev/null
check "backfill SIGINT" $? 0

echo ""

# ============================================================
echo "--- Phase 6: Error Conditions ---"
echo ""

# Bad config
$BINARY monitor --config /nonexistent.yaml >/dev/null 2>&1
check "bad config → exit 1" $? 1

# Invalid range
$BINARY backfill --config "$CONFIG" --from 100 --to 50 >/dev/null 2>&1
check "invalid range → exit 1" $? 1

# Beyond chain height
$BINARY backfill --config "$CONFIG" --from 999999999 --to 999999999 >/dev/null 2>&1
check "beyond chain → exit 1" $? 1

# No duplicate error messages
OUTPUT=$($BINARY monitor --config /nonexistent.yaml 2>&1)
COUNT=$(echo "$OUTPUT" | grep -c "no such file" || true)
check "single error message ($COUNT)" "$COUNT" 1

# No usage in runtime errors
OUTPUT=$($BINARY backfill --config "$CONFIG" --from 100 --to 50 2>&1)
if echo "$OUTPUT" | grep -q "Usage:"; then
    check "no usage in errors" 1 0
else
    check "no usage in errors" 0 0
fi

echo ""

# ============================================================
echo "--- Phase 7: Docker ---"
echo ""

if command -v docker &>/dev/null; then
    echo "  Building Docker image..."
    make docker >/dev/null 2>&1
    check "docker build" $? 0

    # Get the version tag that make docker uses
    DOCKER_TAG=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
    docker run --rm "pocket-settlement-monitor:${DOCKER_TAG}" pocket-settlement-monitor version >/dev/null 2>&1
    check "docker run version" $? 0
else
    skip "docker build" "docker not available"
    skip "docker run version" "docker not available"
fi

echo ""

# ============================================================
# Cleanup
cleanup

echo "=========================================="
echo -e "RESULTS: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}, ${YELLOW}${SKIP} skipped${NC}"
echo "=========================================="

if [ $FAIL -gt 0 ]; then
    exit 1
fi
