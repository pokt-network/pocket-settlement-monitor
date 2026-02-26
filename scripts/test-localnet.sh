#!/usr/bin/env bash
#
# Localnet Integration Test Suite for pocket-settlement-monitor
#
# Usage: ./scripts/test-localnet.sh [RPC_URL]
#
# Prerequisites:
#   - Binary built: make build
#   - Local poktroll node running (localnet or devnet)
#
# Localnet parameters:
#   - Default RPC: tcp://localhost:26657
#   - Block time: varies (typically fast in dev)
#   - Settlement period: every 60 blocks (configurable in genesis)
#   - Designed for relay-miner development workflow
#
# Environment variables:
#   PSM_LOCALNET_RPC    Override RPC URL (default: tcp://localhost:26657)
#   PSM_LOCALNET_PORT   Override metrics port (default: 9093)
#
set -euo pipefail

BINARY="./bin/pocket-settlement-monitor"
CONFIG="config.localnet.yaml"
DB="./settlement-monitor-localnet.db"
RPC="${PSM_LOCALNET_RPC:-${1:-tcp://localhost:26657}}"
METRICS_PORT="${PSM_LOCALNET_PORT:-9093}"

# Derive HTTP URL for curl from the RPC URL
# tcp://host:port -> http://host:port
# http[s]://host  -> as-is
if [[ "$RPC" == tcp://* ]]; then
    HTTP_RPC="http://${RPC#tcp://}"
elif [[ "$RPC" == http://* ]] || [[ "$RPC" == https://* ]]; then
    HTTP_RPC="$RPC"
else
    HTTP_RPC="http://$RPC"
fi

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
    pkill -f "pocket-settlement-monitor.*localnet" 2>/dev/null || true
}

# Find a settlement block by scanning backwards
find_settlement_block() {
    local latest="$1"
    local candidate=$latest

    echo -n "  Scanning for recent settlement block..."
    for i in $(seq 0 5 300); do
        candidate=$((latest - i))
        [ "$candidate" -le 0 ] && break
        local data
        data=$(curl -s --max-time 5 "${HTTP_RPC}/block_results?height=${candidate}" 2>/dev/null)
        if echo "$data" | grep -q "EventClaimSettled" 2>/dev/null; then
            echo -e " ${GREEN}found${NC}: block $candidate"
            echo "$candidate"
            return 0
        fi
    done

    echo -e " ${YELLOW}none in last 300 blocks${NC}"
    echo ""
    return 1
}

# ============================================================
echo "=========================================="
echo "LOCALNET INTEGRATION TEST SUITE"
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

# Generate config if it doesn't exist
if [ ! -f "$CONFIG" ]; then
    echo -e "  ${YELLOW}Config not found, generating $CONFIG...${NC}"
    cat > "$CONFIG" <<EOF
cometbft:
  rpc_url: "${RPC}"
  reconnect_base_delay: "1s"
  reconnect_max_delay: "10s"

suppliers:
  keys_file: ""
  addresses: []

database:
  path: "./${DB}"
  wal_mode: true
  retention: "0"

metrics:
  enabled: true
  addr: ":${METRICS_PORT}"
  labels:
    include_supplier: true
    include_service: true
    include_application: true

backfill:
  delay: "50ms"
  progress_interval: 50

logging:
  level: "debug"
  format: "console"
EOF
    echo -e "  ${GREEN}Generated${NC}: $CONFIG"
else
    echo "  Config: $CONFIG"
fi

# Check localnet RPC connectivity
echo -n "  RPC connectivity ($HTTP_RPC): "
STATUS=$(curl -s --max-time 5 "${HTTP_RPC}/status" 2>/dev/null)
if [ -z "$STATUS" ]; then
    echo -e "${RED}FAILED${NC}"
    echo ""
    echo -e "  ${YELLOW}Is your local node running?${NC}"
    echo "  Start localnet:  make localnet_up  (in poktroll repo)"
    echo "  Or set custom:   PSM_LOCALNET_RPC=tcp://host:port ./scripts/test-localnet.sh"
    exit 1
fi
NETWORK=$(echo "$STATUS" | python3 -c "import json,sys; print(json.load(sys.stdin)['result']['node_info']['network'])" 2>/dev/null)
LATEST=$(echo "$STATUS" | python3 -c "import json,sys; print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null)
echo -e "${GREEN}OK${NC} (network: $NETWORK, height: $LATEST)"

# Auto-detect settlement block
SETTLEMENT_BLOCK=$(find_settlement_block "$LATEST")
if [ -z "$SETTLEMENT_BLOCK" ]; then
    echo -e "  ${YELLOW}No settlements found — chain may be too young or no claims submitted yet.${NC}"
    echo -e "  ${YELLOW}Skipping backfill/query tests. Monitor + error tests will still run.${NC}"
    HAS_SETTLEMENT=false
else
    HAS_SETTLEMENT=true
fi

echo ""

# Clean up before tests
cleanup

# ============================================================
echo "--- Phase 1: RPC Validation ---"
echo ""

# Verify block_results endpoint works
echo -n "  Checking RPC block_results (block $LATEST): "
BLOCK_DATA=$(curl -s --max-time 5 "${HTTP_RPC}/block_results?height=${LATEST}" 2>/dev/null)
if [ -z "$BLOCK_DATA" ]; then
    echo -e "${RED}FAILED — no response${NC}"
    FAIL=$((FAIL+1))
else
    echo -e "${GREEN}OK${NC}"
    PASS=$((PASS+1))
fi

echo ""

# ============================================================
echo "--- Phase 2: Backfill ---"
echo ""

if [ "$HAS_SETTLEMENT" = true ]; then
    # Backfill single settlement block
    echo "  Backfilling block $SETTLEMENT_BLOCK..."
    BACKFILL_OUTPUT=$($BINARY backfill --config "$CONFIG" --from "$SETTLEMENT_BLOCK" --to "$SETTLEMENT_BLOCK" 2>&1)
    BACKFILL_EXIT=$?
    check "backfill settlement block" $BACKFILL_EXIT 0

    if [ $BACKFILL_EXIT -eq 0 ]; then
        EVENTS_FOUND=$(echo "$BACKFILL_OUTPUT" | grep -oP 'events_found=\K\d+' 2>/dev/null || true)
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

    # Backfill a small range
    echo ""
    FROM=$((SETTLEMENT_BLOCK > 2 ? SETTLEMENT_BLOCK - 2 : 1))
    TO=$((SETTLEMENT_BLOCK + 2))
    [ "$TO" -gt "$LATEST" ] && TO=$LATEST
    echo "  Backfilling range $FROM to $TO..."
    $BINARY backfill --config "$CONFIG" --from "$FROM" --to "$TO" >/dev/null 2>&1
    check "backfill range" $? 0

    # Deduplication
    echo ""
    echo "  Re-backfilling block $SETTLEMENT_BLOCK (deduplication test)..."
    $BINARY backfill --config "$CONFIG" --from "$SETTLEMENT_BLOCK" --to "$SETTLEMENT_BLOCK" >/dev/null 2>&1
    check "deduplication (re-backfill)" $? 0
else
    # Backfill a range of non-settlement blocks — should succeed with 0 events
    echo "  Backfilling non-settlement range (last 5 blocks)..."
    FROM=$((LATEST > 5 ? LATEST - 5 : 1))
    $BINARY backfill --config "$CONFIG" --from "$FROM" --to "$LATEST" >/dev/null 2>&1
    check "backfill (no settlements)" $? 0

    skip "events persisted" "no settlement blocks found"
    skip "deduplication" "no settlement blocks found"
fi

echo ""

# ============================================================
echo "--- Phase 3: Query Validation ---"
echo ""

# Queries should succeed even with no data (empty results, not errors)
$BINARY query settlements --config "$CONFIG" --limit 5 >/dev/null 2>&1
check "query settlements" $? 0

$BINARY query summaries --config "$CONFIG" >/dev/null 2>&1
check "query summaries" $? 0

$BINARY query reimbursements --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query reimbursements" $? 0

$BINARY query overservice --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query overservice" $? 0

$BINARY query slashes --config "$CONFIG" --limit 3 >/dev/null 2>&1
check "query slashes" $? 0

$BINARY query settlements --config "$CONFIG" --output csv --limit 3 >/dev/null 2>&1
check "query csv output" $? 0

$BINARY query settlements --config "$CONFIG" --output json --limit 3 >/dev/null 2>&1
check "query json output" $? 0

echo ""

# ============================================================
echo "--- Phase 4: Live Monitor (WebSocket) ---"
echo ""

echo "  Starting monitor (8s test)..."
$BINARY monitor --config "$CONFIG" 2>&1 &
MON_PID=$!
sleep 6

# Check health endpoint
HEALTH=$(curl -s --max-time 3 "http://localhost:${METRICS_PORT}/health" 2>/dev/null)
if echo "$HEALTH" | grep -q '"ok"'; then
    check "health endpoint" 0 0
else
    check "health endpoint" 1 0
fi

# Check ready endpoint
READY=$(curl -s --max-time 3 "http://localhost:${METRICS_PORT}/ready" 2>/dev/null)
if echo "$READY" | grep -q '"ok"'; then
    check "ready endpoint" 0 0
else
    check "ready endpoint" 1 0
fi

# Check metrics endpoint
METRICS=$(curl -s --max-time 3 "http://localhost:${METRICS_PORT}/metrics" 2>/dev/null)
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
PID=$!; sleep 3; kill -INT $PID; wait $PID 2>/dev/null
check "monitor SIGINT" $? 0

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
# Cleanup
cleanup

echo "=========================================="
echo -e "RESULTS: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}, ${YELLOW}${SKIP} skipped${NC}"
echo "=========================================="

if [ $FAIL -gt 0 ]; then
    exit 1
fi
