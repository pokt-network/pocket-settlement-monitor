# Gap Recovery

## Why Gaps Happen

The monitor can miss blocks when:
1. The monitor process itself is down (restart, crash, deployment)
2. The CometBFT fullnode is down or unreachable
3. WebSocket disconnects (network issue, load balancer timeout)
4. The monitor falls behind under extreme load

## Recovery Strategy: Parallel Live + Backfill

### On Startup or Reconnect

```
1. Read last_processed_height from processed_blocks table
2. If DB is empty: set start = current chain height (no going back to genesis)
3. Start WebSocket immediately → writes blocks N+gap, N+gap+1, ...
4. Spawn backfill goroutine → fills gap via /block_results (bounded)
5. Both paths use INSERT OR IGNORE (UNIQUE constraint deduplicates)
6. Backfill finishes → gap closed, only WebSocket continues
```

### Deduplication

The `UNIQUE` constraints on each table prevent duplicate entries:
- `settlements`: `UNIQUE(block_height, event_type, supplier_operator_address, application_address, session_end_block_height)`
- `overservice_events`: `UNIQUE(block_height, application_address, supplier_operator_address)`
- `reimbursement_events`: `UNIQUE(block_height, application_address, supplier_operator_address, session_id)`
- `processed_blocks`: `PRIMARY KEY(height)`

Both live and backfill paths use `INSERT OR IGNORE`, so whichever writes first wins; the second is silently skipped.

### Source Tracking

Each processed block records its source:
- `source='live'`: From WebSocket real-time delivery
- `source='backfill'`: From /block_results historical query

This distinction is critical for Prometheus metric integrity (see below).

## First Run Behavior

Empty database with no `--from` flag = **start from current chain height**. No attempting to index history from genesis.

When `--from` is provided, the monitor starts live immediately **and** backfills from the specified height in parallel:

```bash
# Fresh start: live monitor + backfill from height 500000
pocket-settlement-monitor monitor --config config.yaml --from 500000

# Same with a date (resolved to block height via binary search)
pocket-settlement-monitor monitor --config config.yaml --from 2024-02-15
```

This runs as a single process with one SQLite connection — no need for separate backfill containers. The backfill goroutine runs alongside the live WebSocket subscription using the existing errgroup, and both use `INSERT OR IGNORE` for deduplication.

If the database already has data, `--from` extends history backwards. Blocks already in the database are silently skipped via `INSERT OR IGNORE`. On subsequent restarts without `--from`, normal gap detection resumes from the last processed height.

### Behavior matrix

| DB State | `--from` | Result |
|----------|----------|--------|
| Empty | not set | Start from current height (no backfill) |
| Empty | set | Backfill from `--from` to current height + monitor live |
| Has data | not set | Normal gap detection (lastHeight+1 to current) |
| Has data | earlier than lastHeight | Extend history backwards + fill gap to current |

## Standalone Backfill Command

```bash
pocket-settlement-monitor backfill --from 1000 --to 2000
```

- Writes to SQLite only (settlements, overservice, processed_blocks)
- Hourly/daily summaries are recalculated for affected periods
- **Prometheus counters are NOT incremented** (avoids fake rate spikes)
- Progress logged every 100 blocks
- Does **not** start live monitoring — exits on completion

## Prometheus During Recovery

**Rule: Prometheus counters increment ONLY from the live WebSocket path, NEVER from backfill.**

Why: If we incremented counters during backfill, Prometheus `rate()` queries would show massive fake spikes at recovery time, making alerting unreliable.

### What gets updated:
- Live events: All counters + all gauges
- Backfill events: Gauges only (`psm_last_processed_height`, `psm_backfill_blocks_remaining`)
- Both: SQLite tables (the complete truth)

### Operational gauges for visibility:
- `psm_last_processed_height` — latest height in SQLite
- `psm_current_block_height` — latest height from WebSocket
- `psm_backfill_blocks_remaining` — 0 when caught up
- `psm_gap_detected_total` — counter of gap events

## Reconnection Flow

```
1. WebSocket disconnects
2. subscriber.Client enters reconnect loop:
     1s → 2s → 4s → 8s → 16s → 30s (cap), ±20% jitter
3. On successful reconnect:
   a. Record last_processed_height from SQLite
   b. Query current chain height via RPC
   c. If gap > 0:
      - Spawn backfill goroutine for missing heights
      - Live stream continues concurrently
      - Both use INSERT OR IGNORE
      - psm_gap_detected_total.Inc()
      - psm_backfill_blocks_remaining set to gap size
   d. If gap == 0: nothing to do, live continues
4. Backfill finishes → psm_backfill_blocks_remaining = 0
5. Summaries for affected hours are recalculated
```

## Summary Recalculation After Backfill

When backfill fills in data for past hours/days, the affected summaries must be recalculated:

1. Find all distinct `hour_start` / `day_date` values in the backfilled range
2. For each affected period, re-aggregate from `settlements` table
3. Use `INSERT OR REPLACE` to update existing summary rows
4. Only send Discord notifications for the current (live) hour/day, not historical ones

## Related Documentation

- [Architecture](architecture.md) — System design and component responsibilities
- [Database Schema](database-schema.md) — UNIQUE constraints that enable deduplication
- [Metrics Reference](metrics-reference.md) — Operational metrics for monitoring gaps and backfill
- [Troubleshooting](troubleshooting.md) — Backfill timeout and failure recovery
