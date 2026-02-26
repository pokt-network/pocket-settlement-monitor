# Architecture

## Purpose

`pocket-settlement-monitor` is a standalone tool that monitors Pocket Network (poktroll) tokenomics settlement events. It replaces the `SettlementMonitor` component in pocket-relay-miner, which consumed 70%+ RAM by polling `/block_results` (1GB+ per block on mainnet).

## Design Principles

1. **WebSocket-first**: Subscribe to CometBFT `NewBlockEvents` (~KB) instead of polling `/block_results` (~GB)
2. **Standalone**: Runs as its own process/container, fully decoupled from the relay-miner
3. **Universal**: Usable by relay-miner operators, explorers, validators, and Pocket Network Foundation
4. **Observable**: Prometheus metrics for real-time monitoring, SQLite for historical queries

## System Overview

```
                                    pocket-settlement-monitor
                                    ┌────────────────────────────────────────────┐
                                    │                                            │
CometBFT Node ──WebSocket──────────►  subscriber.Client                         │
(NewBlockEvents)                    │    │                                       │
                                    │    ▼                                       │
                                    │  processor.Collector                       │
                                    │  (accumulate per block)                    │
                                    │    │                                       │
                                    │    ▼ on height change                      │
                                    │  processor.Processor                       │
                                    │    ├── filter by supplier (optional)       │
                                    │    ├── correlate overservice (in-memory)   │
                                    │    ├── compute estimated relays            │
                                    │    ├── batch INSERT → SQLite              │
                                    │    ├── increment → Prometheus (live only)  │
                                    │    ├── check hour/day boundary → summary  │
                                    │    └── dispatch → Discord (async)         │
                                    │                                            │
                                    │  metrics.Server (:9090)                    │
                                    │    /metrics, /health, /ready               │
                                    │                                            │
                                    │  store.SQLite (settlement-monitor.db)      │
                                    │    settlements, summaries, processed_blocks│
                                    │                                            │
                                    │  notify.Discord (webhook, rate-limited)    │
                                    └────────────────────────────────────────────┘
```

## Component Responsibilities

### subscriber (WebSocket Client)
- Connects to CometBFT RPC via WebSocket
- Subscribes to `tm.event = 'NewBlockEvents'`
- Decodes ABCI events into typed proto messages using `ParseTypedEvent`
- Handles reconnection with exponential backoff + jitter
- Emits `SettlementEvent` structs on a buffered channel

### processor (Event Processing Pipeline)
- **Collector**: Accumulates all events for a single block height before flushing
- **Processor**: Correlates overservice events with settled claims (same block), computes estimated relays, dispatches to store/metrics/notifications
- **Filter**: Optionally filters events by supplier address (or passes all in monitor-all mode)
- **Reporter**: Detects hour/day boundaries in block timestamps, materializes summary rows

### store (SQLite Persistence)
- Schema with settlements, overservice_events, reimbursement_events, processed_blocks, hourly/daily summaries
- Batch inserts per block (single transaction)
- Gap detection via processed_blocks table
- Retention cleanup goroutine
- Pure Go SQLite (modernc.org/sqlite) — no CGO required

### metrics (Prometheus)
- All counters incremented ONLY from live WebSocket path (never backfill)
- Gauges for operational visibility (heights, connection status, backfill progress)
- HTTP server serving /metrics, /health, /ready

### notify (Discord Webhooks)
- Async dispatch via buffered channel (64 messages)
- Discord 429 rate limit handling with automatic retry (up to 3 attempts)
- Color-coded embeds: red (slash), dark orange (expiration), green (settlement), yellow (overservice), blue (summary), gray (discard)
- Three webhook URLs in a single `notifications:` config: default, critical (slashes), ops (connection/gap/health) — each falls back to `webhook_url` when not set
- Graceful shutdown drains remaining messages (5s timeout)

### config
- YAML configuration with sensible defaults
- Supplier address resolution from keys file (hex → bech32) or plain address list
- Monitor-all mode when no suppliers configured

## Deployment Model

- Single binary, single container
- One SQLite file for persistence
- Connects to one CometBFT RPC endpoint
- Optionally sends to Discord webhook(s)
- Exposes Prometheus metrics on configurable port

## WebSocket vs block_results

| Aspect | block_results (old) | NewBlockEvents (new) |
|--------|---------------------|----------------------|
| Data size per block | 1GB+ (mainnet) | ~KB (events only) |
| Delivery model | Polling (per block) | Push (real-time) |
| Latency | height-1 workaround needed | Direct delivery |
| Memory usage | 2GB+ with 2 workers | Negligible |
| Connection count | N (one per relayer) | 1 (dedicated monitor) |

## Monitor-All Mode

When no supplier addresses are configured, the monitor tracks **every supplier on the network**. This mode is designed for:
- Pocket Network Foundation (network-wide visibility)
- PocketScan / explorers (indexing all settlement data)
- Validators (monitoring the full settlement pipeline)

Performance is maintained via batch writes per block — all events for a single block are correlated in memory, then written to SQLite in a single transaction.

## Related Documentation

- [Settlement Logic](settlement-logic.md) — Event types, difficulty expansion, overservice correlation
- [Gap Recovery](gap-recovery.md) — Downtime recovery, parallel backfill, deduplication
- [Database Schema](database-schema.md) — SQLite tables, indexes, retention
- [Metrics Reference](metrics-reference.md) — Prometheus metrics, alert rules
- [Troubleshooting](troubleshooting.md) — Common issues and solutions
