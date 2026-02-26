# Metrics Reference

All Prometheus metrics use namespace `psm` (pocket-settlement-monitor).

**Critical Rule**: Counters are incremented ONLY from live WebSocket events. Never from backfill.

## Event Counters

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_claims_settled_total` | counter | event_type, supplier, service, application | Total claims settled |
| `psm_claims_expired_total` | counter | event_type, supplier, service, application | Total claims expired |
| `psm_suppliers_slashed_total` | counter | event_type, supplier, service, application | Total supplier slashes |
| `psm_claims_discarded_total` | counter | event_type, supplier, service, application | Total claims discarded |
| `psm_applications_overserviced_total` | counter | event_type, supplier, service, application | Total overservice events |

## Revenue Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_upokt_earned_total` | counter | event_type, supplier, service, application | Total uPOKT earned from settlements |
| `psm_upokt_claimed_total` | counter | event_type, supplier, service, application | Total uPOKT claimed |
| `psm_upokt_lost_expired_total` | counter | event_type, supplier, service, application | Total uPOKT lost to expirations |
| `psm_upokt_overserviced_total` | counter | _(none)_ | Total uPOKT lost to overservice (expected burn - effective burn) |

## Relay / Compute Unit Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_relays_settled_total` | counter | event_type, supplier, service, application | Merkle tree relays settled (passed difficulty) |
| `psm_estimated_relays_settled_total` | counter | event_type, supplier, service, application | Expanded relay count settled (real throughput) |
| `psm_relays_expired_total` | counter | event_type, supplier, service, application | Merkle tree relays lost |
| `psm_estimated_relays_expired_total` | counter | event_type, supplier, service, application | Expanded relays lost |
| `psm_compute_units_settled_total` | counter | event_type, supplier, service, application | CUs from merkle tree settled |
| `psm_estimated_compute_units_settled_total` | counter | event_type, supplier, service, application | Estimated CUs settled |

## Timing Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_settlement_latency_blocks` | histogram | event_type | Blocks between session end and settlement |

Histogram buckets: 10, 20, 30, 50, 75, 100, 150, 200, 300

Note: Blocks are ~1m (mainnet) or ~30s (testnet).

## Operational Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_blocks_processed_total` | counter | | Total blocks processed |
| `psm_current_block_height` | gauge | | Latest height from WebSocket |
| `psm_last_processed_height` | gauge | | Latest height in SQLite |
| `psm_backfill_blocks_remaining` | gauge | | Blocks left to backfill (0 = caught up) |
| `psm_gap_detected_total` | counter | | Number of gap detection events |
| `psm_websocket_connected` | gauge | | 1 if connected, 0 if disconnected |
| `psm_websocket_reconnects_total` | counter | | Total WebSocket reconnection attempts |
| `psm_suppliers_monitored` | gauge | | Number of supplier addresses being monitored |
| `psm_info` | gauge | version, commit | Set to 1, carries build info |

## Notification Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_discord_notifications_sent_total` | counter | type | Successful Discord webhook sends |
| `psm_discord_notification_errors_total` | counter | type | Failed Discord webhook sends |

Types: settlement, expiration, slash, discard, overservice, hourly_summary, daily_summary

## Store Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `psm_sqlite_operations_total` | counter | operation, status | SQLite operation counts |
| `psm_retention_rows_deleted_total` | counter | table | Rows deleted by retention cleanup |

## Label Cardinality Notes

All event, revenue, and relay/CU counters are registered with four labels: `event_type`, `supplier`, `service`, `application`. The `event_type` label is always populated. The other three are controlled via `metrics.labels` in the config — when disabled, the label value is set to empty string (reducing cardinality while keeping the label key present).

| Label | Config Toggle | Cardinality | Notes |
|-------|--------------|-------------|-------|
| `event_type` | Always | Fixed (6) | Proto event type name |
| `supplier` | `include_supplier` | Low-medium | ~1-50 per operator, safe to enable |
| `service` | `include_service` | Low | ~60 on mainnet, safe to enable |
| `application` | `include_application` | **High** | Thousands on mainnet, avoid unless needed |
| `type` | Always | Fixed (7) | Notification types |
| `operation` | Always | Fixed (~5) | SQLite operation names |
| `status` | Always | Fixed (2) | success, error |

**Recommendation**: Enable `supplier` + `service`, keep `application` disabled on mainnet.

## Example Prometheus Queries

```promql
# Settlement rate per supplier (last 5m)
rate(psm_claims_settled_total[5m])

# Revenue per supplier per hour
increase(psm_upokt_earned_total[1h])

# Real relay throughput (estimated, after difficulty expansion)
rate(psm_estimated_relays_settled_total[5m])

# Expiration rate
rate(psm_claims_expired_total[1h])

# Settlement latency p95
histogram_quantile(0.95, rate(psm_settlement_latency_blocks_bucket[1h]))

# Is monitor connected?
psm_websocket_connected

# Backfill progress
psm_backfill_blocks_remaining

# Block processing lag
psm_current_block_height - psm_last_processed_height

# Total revenue in POKT (divide uPOKT by 1M)
increase(psm_upokt_earned_total[24h]) / 1e6

# Slash rate as percentage of total claims
rate(psm_suppliers_slashed_total[1h]) / rate(psm_claims_settled_total[1h]) * 100

# Notification delivery success rate
rate(psm_discord_notifications_sent_total[1h])
  / (rate(psm_discord_notifications_sent_total[1h]) + rate(psm_discord_notification_errors_total[1h])) * 100
```

## Example Prometheus Alert Rules

Production-ready alerting rules for the settlement monitor:

```yaml
groups:
  - name: pocket-settlement-monitor
    rules:
      # Monitor disconnected for more than 5 minutes
      - alert: PSMWebSocketDisconnected
        expr: psm_websocket_connected == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Settlement monitor WebSocket disconnected"
          description: "psm has been disconnected from CometBFT for more than 5 minutes."

      # Block processing lag exceeds 10 blocks
      - alert: PSMProcessingLag
        expr: (psm_current_block_height - psm_last_processed_height) > 10
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Settlement monitor is falling behind"
          description: "Processing lag is {{ $value }} blocks."

      # Backfill running for more than 30 minutes
      - alert: PSMBackfillStalled
        expr: psm_backfill_blocks_remaining > 0
        for: 30m
        labels:
          severity: warning
        annotations:
          summary: "Backfill has been running for over 30 minutes"
          description: "{{ $value }} blocks remaining."

      # Supplier slashed
      - alert: PSMSupplierSlashed
        expr: increase(psm_suppliers_slashed_total[5m]) > 0
        labels:
          severity: critical
        annotations:
          summary: "Supplier slashed"
          description: "Supplier {{ $labels.supplier }} was slashed on service {{ $labels.service }}."

      # High expiration rate (more than 5 in an hour)
      - alert: PSMHighExpirationRate
        expr: increase(psm_claims_expired_total[1h]) > 5
        labels:
          severity: warning
        annotations:
          summary: "High claim expiration rate"
          description: "{{ $value }} claims expired in the last hour."

      # Discord notifications failing
      - alert: PSMNotificationErrors
        expr: rate(psm_discord_notification_errors_total[15m]) > 0
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Discord notification errors"
          description: "Webhook notifications have been failing for 15+ minutes."

      # No blocks processed (monitor may be stuck)
      - alert: PSMNoBlocksProcessed
        expr: increase(psm_blocks_processed_total[30m]) == 0 and psm_websocket_connected == 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "No settlement blocks processed"
          description: >
            Connected but no settlement blocks processed in 30 minutes.
            On mainnet, settlements occur every ~90 minutes (60 blocks x ~1.5 min),
            so this may be normal. Investigate if this persists beyond 2 hours.

      # SQLite errors
      - alert: PSMSQLiteErrors
        expr: rate(psm_sqlite_operations_total{status="error"}[15m]) > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "SQLite operation errors"
          description: "Database errors detected on operation {{ $labels.operation }}."
```

## Grafana Dashboard Tips

Key panels for a settlement monitor dashboard:

**Overview row**:
- `psm_websocket_connected` — Stat panel (1=connected, 0=disconnected)
- `psm_current_block_height` — Stat panel showing current chain height
- `psm_info` — Stat panel showing version/commit from labels

**Settlement activity row**:
- `rate(psm_claims_settled_total[5m])` — Time series by supplier
- `increase(psm_upokt_earned_total[1h])` — Bar chart of hourly revenue
- `increase(psm_estimated_relays_settled_total[1h])` — Relay throughput

**Health row**:
- `psm_current_block_height - psm_last_processed_height` — Processing lag gauge
- `psm_backfill_blocks_remaining` — Backfill progress
- `rate(psm_websocket_reconnects_total[1h])` — Reconnection frequency

**Issues row**:
- `increase(psm_claims_expired_total[1h])` — Expirations by event type
- `increase(psm_suppliers_slashed_total[1h])` — Slashes
- `rate(psm_discord_notification_errors_total[5m])` — Notification failures
