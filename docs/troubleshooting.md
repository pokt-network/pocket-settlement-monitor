# Troubleshooting

## WebSocket Connection Issues

### "failed to connect to WebSocket" on startup

**Symptoms**: Monitor fails to start or logs connection errors.

**Causes & fixes**:

1. **Wrong RPC URL scheme**: The `cometbft.rpc_url` must use `tcp://`, `http://`, or `https://`.
   ```yaml
   # Correct
   cometbft:
     rpc_url: "tcp://localhost:26657"       # Local node
     rpc_url: "https://rpc.example.com"     # HTTPS-proxied
     rpc_url: "http://localhost:26657"       # HTTP

   # Wrong
   cometbft:
     rpc_url: "ws://localhost:26657"         # Don't use ws://
     rpc_url: "localhost:26657"              # Missing scheme
   ```

2. **Node not running or unreachable**: Verify the CometBFT node is accessible:
   ```bash
   curl http://localhost:26657/status
   # or for HTTPS
   curl https://rpc.example.com/status
   ```

3. **Firewall or load balancer**: WebSocket connections require HTTP upgrade support. Some reverse proxies (nginx, cloud LBs) need explicit WebSocket configuration.

### Frequent reconnections

**Symptoms**: Repeated `websocket disconnected` / `websocket reconnected` log entries.

**Causes**:
- Unstable network between monitor and CometBFT node
- Load balancer idle timeout shorter than block time (~60s mainnet)
- CometBFT node under heavy load

**Tuning**: Adjust reconnection backoff:
```yaml
cometbft:
  reconnect_base_delay: "2s"    # Start slower
  reconnect_max_delay: "60s"    # Allow longer waits
```

## Database Issues

### "database is locked"

**Symptoms**: SQLite operations fail with `SQLITE_BUSY` or `database is locked`.

**Causes**:
- Another process has the database file open (e.g., running two monitor instances against the same DB file)
- The busy timeout (5000ms) was exceeded under heavy write load

**Fixes**:
- Ensure only one monitor instance uses a given database file
- Verify WAL mode is enabled (default): `database.wal_mode: true`
- Check that no external tool (DB browser, sqlite3 CLI) holds a write lock

### Database grows very large

**Symptoms**: Database file exceeds expected size.

**Causes**:
- Retention disabled (`retention: "0"`) or set very high
- Monitor-all mode on mainnet produces thousands of events per settlement block
- WAL file (`-wal`) can temporarily grow large during heavy writes

**Fixes**:
- Set a reasonable retention: `database.retention: "360h"` (15 days)
- Retention cleanup runs hourly; summaries are kept 3-6x longer than raw events
- The WAL file is checkpointed automatically by SQLite

### Corrupt database

**Symptoms**: Schema errors or unexpected query failures.

**Recovery**:
1. Stop the monitor
2. Back up the database file: `cp settlement-monitor.db settlement-monitor.db.bak`
3. Check integrity: `sqlite3 settlement-monitor.db "PRAGMA integrity_check;"`
4. If corrupt, delete and restart (data will be lost; use backfill to repopulate):
   ```bash
   rm settlement-monitor.db
   pocket-settlement-monitor monitor --config config.yaml
   # Then backfill the needed range
   pocket-settlement-monitor backfill --config config.yaml --from <start> --to <end>
   ```

## Notification Issues

### Discord notifications not sending

**Symptoms**: No messages in Discord channel despite settlement events being processed.

**Checklist**:
1. **Webhook URL set?** At least `notifications.webhook_url` must be non-empty
2. **Toggles enabled?** Check which notification types are enabled:
   ```yaml
   notifications:
     notify_settlements: false  # Note: default is false
     notify_expirations: true
     notify_slashes: true
   ```
3. **Backfill doesn't notify**: Notifications only fire from live WebSocket events, not from backfill
4. **Channel full?** Check logs for `notification channel full, dropping message`
5. **Rate limited?** Check logs for `discord rate limited, waiting`

### Webhook errors in logs

**Symptoms**: `failed to send discord notification` with HTTP status codes.

| Status | Meaning | Fix |
|--------|---------|-----|
| 401 | Invalid webhook URL | Regenerate webhook in Discord channel settings |
| 404 | Webhook deleted | Create a new webhook and update config |
| 429 | Rate limited | Automatic retry (up to 3 attempts). If persistent, reduce notification volume |

### Testing webhooks locally

Use the built-in mock server:
```bash
make mock-webhook    # Starts on :9092
```

Then point your config at it:
```yaml
notifications:
  webhook_url: "http://localhost:9092/webhook"
```

## Metrics Issues

### High cardinality / Prometheus scrape slow

**Symptoms**: Prometheus scrapes take >10s, or metric count explodes.

**Cause**: The `include_application` label is enabled. With thousands of unique application addresses on mainnet, this creates a combinatorial explosion of time series.

**Fix**: Disable application labels (they're off by default for this reason):
```yaml
metrics:
  labels:
    include_supplier: true
    include_service: true
    include_application: false   # Keep this false on mainnet
```

### Counters show zero despite processing events

**Symptoms**: `psm_claims_settled_total` stays at 0 even though blocks are being processed.

**Causes**:
- Running in backfill mode (counters only increment from live events)
- No settlement blocks have occurred yet (settlements happen every 60 blocks)
- Filtering by supplier but no events match your supplier addresses

**Check**: Look at operational metrics first:
```promql
psm_blocks_processed_total       # Should be incrementing
psm_current_block_height         # Should match chain height
psm_websocket_connected          # Should be 1
```

### Port already in use

**Symptoms**: `listen tcp :9090: bind: address already in use`

**Fix**: Change the metrics port in config:
```yaml
metrics:
  addr: ":9091"   # Use a different port
```

Common port assignments for local development:
- `:9090` — Default / beta testnet
- `:9091` — Mainnet instance
- `:9092` — Mock webhook server

## Backfill Issues

### Backfill times out on mainnet

**Symptoms**: Block fetches during backfill fail with timeout errors, especially for settlement blocks.

**Cause**: Mainnet settlement blocks produce 1GB+ `/block_results` responses. Some blocks take 2-4 HTTP attempts to fetch.

**Fixes**:
- Increase the delay between fetches to reduce load:
  ```yaml
  backfill:
    delay: "500ms"    # Slower but more reliable on mainnet
  ```
- Use a local or low-latency CometBFT node for backfill
- Backfill smaller ranges at a time

### Backfill doesn't seem to do anything

**Symptoms**: Backfill completes but no data in queries.

**Causes**:
- The specified range has no settlement blocks (settlements only happen every 60 blocks)
- Date range resolved to zero blocks (dates use binary search on block timestamps)
- Data already existed (deduplication via `INSERT OR IGNORE`)

**Check**: Use backfill with progress logging:
```bash
pocket-settlement-monitor backfill --config config.yaml \
  --from 646200 --to 646300
```

Look for `blocks processed` / `settlement events found` in the output.

## Configuration Issues

### "invalid config" on startup

The config validator checks for these common mistakes:
- `cometbft.rpc_url` is empty
- `cometbft.rpc_url` uses an invalid scheme (must be `tcp://`, `http://`, or `https://`)
- `cometbft.reconnect_base_delay` > `cometbft.reconnect_max_delay`
- `database.path` is empty
- `metrics.addr` is empty when metrics are enabled
- `database.retention` is negative

### Supplier keys file errors

**Format**: The keys file must be YAML with a `keys` list of hex-encoded secp256k1 private keys:

```yaml
# supplier-keys.yaml
keys:
  - "aabbccdd..."   # 64 hex chars (32 bytes)
  - "0xeeff0011..."  # 0x prefix is optional
```

**Common errors**:
- `invalid hex`: Key contains non-hex characters
- `expected 32 bytes, got N`: Key is wrong length (must be exactly 64 hex chars / 32 bytes)
- `keys file has no keys`: File exists but `keys` list is empty

### Monitor-all mode not working as expected

If both `suppliers.keys_file` and `suppliers.addresses` are empty, the monitor tracks ALL suppliers on the network. This is intentional. To filter:
```yaml
suppliers:
  addresses:
    - "pokt1abc..."
    - "pokt1def..."
```

## Operational Issues

### Process won't shut down cleanly

**Symptoms**: Monitor hangs on SIGINT/SIGTERM.

The monitor has a graceful shutdown sequence:
1. Cancel context (stops WebSocket subscription)
2. Flush remaining block events in the collector
3. Drain notification queue (up to 5s timeout)
4. Close database connection

If shutdown hangs beyond ~10s, send a second SIGINT or SIGKILL.

### Log output is too verbose / not verbose enough

Adjust the log level:
```yaml
logging:
  level: "info"     # debug, info, warn, error
  format: "console"  # "console" for human-readable, "json" for structured
```

- **debug**: Shows per-event detail, unexpected event types, decoder internals
- **info**: Connections, block processing, summaries (recommended for production)
- **warn**: Dropped notifications, rate limiting, non-critical failures
- **error**: Failed operations that need attention
