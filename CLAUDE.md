# CLAUDE.md — pocket-settlement-monitor

## What This Project Is

A standalone tool that monitors Pocket Network (poktroll) tokenomics settlement events via CometBFT WebSocket. It replaces the SettlementMonitor component from pocket-relay-miner, which caused OOM issues on mainnet (polling /block_results returns 1GB+ per block).

## Knowledge Base

All specifications and business logic are documented in `docs/`:
- **`docs/architecture.md`** — System overview, component diagram, data flow
- **`docs/settlement-logic.md`** — Critical business logic: all 6 event types, estimated relays, overservice correlation, ABCI decoding
- **`docs/gap-recovery.md`** — Downtime recovery, parallel live+backfill, deduplication
- **`docs/metrics-reference.md`** — All Prometheus metrics with descriptions, alert rules, and Grafana tips
- **`docs/database-schema.md`** — SQLite tables, indexes, retention, entity relationships
- **`docs/troubleshooting.md`** — Common issues and solutions
- **`CONTRIBUTING.md`** — Code standards, testing approach, PR guidelines

**Read `docs/settlement-logic.md` before modifying event processing code.**

## Core Principles

- **Production software**: Must handle mainnet loads efficiently
- **WebSocket-first**: Use `NewBlockEvents` (~KB) not `/block_results` (~GB)
- **Prometheus integrity**: Counters increment ONLY from live events, never backfill
- **SQLite batch writes**: All events for a block in a single transaction
- **Overservice correlation**: In-memory per block (same EndBlocker), no DB lookups

## Code Standards

- Same as pocket-relay-miner (see its CLAUDE.md)
- **Error handling**: Always check, wrap with context
- **Logging**: zerolog structured logging, `ForComponent` for sub-loggers
- **Testing**: Real implementations (in-memory SQLite for tests), no mocks
- **No CGO**: Use `modernc.org/sqlite` (pure Go)

## Building & Running

```bash
make build          # Development build
make build-release  # Production build (CGO_ENABLED=0, stripped)
make test           # Run all tests with -race
make fmt lint       # Format and lint
make docker         # Build Docker image

# Run against beta testnet
./bin/pocket-settlement-monitor monitor --config config.beta.yaml

# Monitor live + backfill from a specific height (single process)
./bin/pocket-settlement-monitor monitor --config config.beta.yaml --from 60000

# Backfill historical blocks (standalone, no live monitoring)
./bin/pocket-settlement-monitor backfill --config config.beta.yaml --from 60750 --to 60760

# Query settlement data
./bin/pocket-settlement-monitor query settlements --config config.beta.yaml --limit 10
./bin/pocket-settlement-monitor query summaries --config config.beta.yaml
```

## RPC URL Formats

The `cometbft.rpc_url` config accepts:
- `tcp://host:26657` — Local/internal CometBFT node
- `https://host` — HTTPS-proxied RPC (e.g., beta/mainnet infra)
- `http://host:26657` — HTTP RPC

## Tested Environments

- **Beta testnet**: `https://sauron-rpc.beta.infra.pocket.network` (network: `pocket-lego-testnet`)
  - Blocks every ~30s, settlements every 60 blocks
  - Service: `pnf-anvil`, 32 suppliers per settlement block
  - All 6 event types use proto names: `pocket.tokenomics.EventClaimSettled` etc.

## CI/CD

- **GitHub Actions**: `.github/workflows/ci.yml` (lint, test, build, docker)
- **Release workflow**: `.github/workflows/release.yml` (tag-triggered, lint+test, cross-platform binaries, checksums, GHCR image)
- **Docker image**: Multi-stage Alpine build, ~22MB, non-root user, tini init
- **Versioning**: Semver with `v` prefix (`v1.0.0`, `v1.1.0`). Version injected via ldflags at build time

## Project Structure

```
main.go                    # Entry point
cmd/                       # Cobra commands (root, monitor, version, backfill, query)
config/                    # Config loading and supplier address resolution
subscriber/                # CometBFT WebSocket client, event decoder
processor/                 # Event collector, processor, filter, reporter
store/                     # SQLite schema, migrations, CRUD, retention
notify/                    # Discord webhook notifications (settlement + ops)
metrics/                   # Prometheus metrics and HTTP server
logging/                   # zerolog wrapper
internal/version/          # Build version vars (ldflags)
docs/                      # Specification documents
```

## Dependencies

Key deps (same versions as pocket-relay-miner):
- `cometbft` — WebSocket subscription, ABCI types
- `cosmos-sdk` — ParseTypedEvent, secp256k1 crypto
- `poktroll` — Tokenomics event type definitions
- `zerolog` — Structured logging
- `cobra` — CLI framework
- `prometheus/client_golang` — Metrics
- `modernc.org/sqlite` — Pure Go SQLite (no CGO)

## Counter-Party

This project works alongside:
- **pocket-relay-miner** (`../pocket-relay-miner`) — The relay miner that this tool complements
- **PATH** (`../path`) — The gateway that generates relays being settled

## Related Source Code

Event handling was ported from pocket-relay-miner:
- `miner/pocketd_events.go` → `subscriber/` (WebSocket client, decoder, filterEventAttrs)
- `miner/settlement_monitor.go` → `processor/` (event handling patterns)
- `keys/supplier_keys_file.go` → `config/suppliers.go` (key derivation)
- `cmd/events/main.go` → reference for all 6 event types and their fields
