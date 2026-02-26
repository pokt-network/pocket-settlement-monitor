# pocket-settlement-monitor

A lightweight tool that monitors [Pocket Network](https://pokt.network) (poktroll) tokenomics settlement events in real-time via CometBFT WebSocket, persisting them to SQLite and exposing Prometheus metrics and Discord notifications.

Built as a replacement for the `SettlementMonitor` component in [pocket-relay-miner](https://github.com/pokt-network/pocket-relay-miner), which caused OOM issues on mainnet by polling `/block_results` (~1GB per settlement block). This tool uses WebSocket `NewBlockEvents` (~KB per block) instead.

## Features

- **Real-time monitoring** via CometBFT WebSocket subscription (`NewBlockEvents`)
- **All 6 settlement event types**: settled, expired, slashed, discarded, overserviced, reimbursement
- **SQLite persistence** with batch writes per block and automatic retention
- **Prometheus metrics** with configurable labels (counters increment from live events only)
- **Discord notifications** with color-coded embeds, hourly/daily summaries, and rate limiting
- **Operational alerts** via separate Discord webhook (connection, gaps, health)
- **Gap recovery** with automatic detection and parallel live + backfill
- **Historical backfill** by block height range or date range
- **Query CLI** with table, JSON, and CSV output formats
- **Monitor-all mode** for tracking every supplier on the network
- **Supplier key derivation** from hex private keys to bech32 addresses
- **Pure Go** — no CGO dependency (`modernc.org/sqlite`)
- **~22MB Docker image** with multi-stage Alpine build

## Quick Start

```bash
# Build
make build

# Copy and edit config
cp config.example.yaml config.yaml

# Monitor (live WebSocket)
./bin/pocket-settlement-monitor monitor --config config.yaml

# Backfill historical blocks
./bin/pocket-settlement-monitor backfill --config config.yaml --from 60750 --to 60760

# Query data
./bin/pocket-settlement-monitor query settlements --config config.yaml --limit 10
./bin/pocket-settlement-monitor query summaries --config config.yaml --period daily
```

## Installation

### From Source

Requires Go 1.25+.

```bash
git clone https://github.com/pokt-network/pocket-settlement-monitor.git
cd pocket-settlement-monitor
make build          # Development build
make build-release  # Production build (stripped, CGO_ENABLED=0)
```

The binary is written to `bin/pocket-settlement-monitor`.

### Docker

Build the image locally:

```bash
make docker
```

Run with config and data volumes:

```bash
docker run -d \
  --name psm \
  -p 9090:9090 \
  -v $(pwd)/config.yaml:/etc/psm/config.yaml:ro \
  -v psm-data:/home/psm/data \
  pocket-settlement-monitor:$(git describe --tags --always --dirty) \
  monitor --config /etc/psm/config.yaml
```

The Docker image:
- Multi-stage build: Go 1.25.7 builder + Alpine 3.21 runtime
- ~22MB image size
- Runs as non-root user (`psm`)
- Uses [tini](https://github.com/krallin/tini) as init for proper signal handling
- Default command: `monitor --config /etc/psm/config.yaml`

**Important**: Mount the database path as a volume (`-v psm-data:/home/psm/data`) and set `database.path: "/home/psm/data/settlement-monitor.db"` in your config so data persists across container restarts.

### GitHub Releases

Pre-built binaries for linux/darwin (amd64/arm64) and Docker images on GHCR are published on tagged releases.

```bash
# Pull from GHCR
docker pull ghcr.io/pokt-network/pocket-settlement-monitor:v1.0.0
docker pull ghcr.io/pokt-network/pocket-settlement-monitor:latest
```

## Configuration

See [`config.example.yaml`](config.example.yaml) for all options with inline comments.

```yaml
cometbft:
  rpc_url: "tcp://localhost:26657"    # tcp://, https://, or http://
  reconnect_base_delay: "1s"
  reconnect_max_delay: "30s"

suppliers:
  keys_file: ""       # Path to supplier-keys.yaml (hex keys -> bech32)
  addresses: []       # List of pokt1... addresses (leave both empty for monitor-all)

database:
  path: "./settlement-monitor.db"
  wal_mode: true
  retention: "360h"   # 15 days; 0 = keep forever

metrics:
  enabled: true
  addr: ":9090"
  labels:
    include_supplier: true
    include_service: true
    include_application: false   # high cardinality warning

backfill:
  delay: "100ms"             # delay between block fetches
  progress_interval: 100     # log progress every N blocks

notifications:
  webhook_url: ""                  # default / settlement webhook
  critical_webhook_url: ""         # slashes (falls back to webhook_url)
  ops_webhook_url: ""              # operational alerts (falls back to webhook_url)
  notify_settlements: false
  notify_expirations: true
  notify_slashes: true
  notify_discards: true
  notify_overservice: true
  hourly_summary: true
  daily_summary: true
  notify_connection: true
  notify_gap: true
  notify_health: true

logging:
  level: "info"                  # debug, info, warn, error
  format: "json"                 # json or console
```

### Configuration Reference

#### `cometbft`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rpc_url` | string | `tcp://localhost:26657` | CometBFT RPC endpoint. Schemes: `tcp://` (local), `https://` (proxied), `http://` |
| `reconnect_base_delay` | duration | `1s` | Initial reconnection delay after WebSocket disconnect |
| `reconnect_max_delay` | duration | `30s` | Maximum reconnection delay (exponential backoff caps here) |

#### `suppliers`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `keys_file` | string | `""` | Path to a YAML file with hex private keys (see [Supplier Keys File](#supplier-keys-file)) |
| `addresses` | list | `[]` | List of `pokt1...` bech32 addresses to monitor |

Both sources are merged and deduplicated. If both are empty, the monitor enters **monitor-all mode** (tracks every supplier on the network).

#### `database`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `./settlement-monitor.db` | SQLite database file path. Use `:memory:` for testing |
| `wal_mode` | bool | `true` | Enable WAL journal mode (recommended for concurrent reads) |
| `retention` | duration | `720h` (30 days) | Delete rows older than this. Summaries kept 3-6x longer. `0` = keep forever. `config.example.yaml` uses `360h` (15 days) |

#### `metrics`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable Prometheus metrics HTTP server |
| `addr` | string | `:9090` | Listen address for metrics server |
| `labels.include_supplier` | bool | `false` | Add `supplier` label to metrics |
| `labels.include_service` | bool | `false` | Add `service` label to metrics |
| `labels.include_application` | bool | `false` | Add `application` label (**high cardinality on mainnet**) |

#### `notifications`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `webhook_url` | string | `""` | Default Discord webhook URL. Empty = notifications disabled |
| `critical_webhook_url` | string | `""` | Separate webhook for slashes. Falls back to `webhook_url` |
| `ops_webhook_url` | string | `""` | Separate webhook for operational alerts. Falls back to `webhook_url` |
| `notify_settlements` | bool | `false` | Notify on successful settlements |
| `notify_expirations` | bool | `true` | Notify on claim expirations |
| `notify_slashes` | bool | `true` | Notify on supplier slashes |
| `notify_discards` | bool | `true` | Notify on claim discards |
| `notify_overservice` | bool | `true` | Notify on overservice events |
| `hourly_summary` | bool | `true` | Post hourly aggregation summary |
| `daily_summary` | bool | `true` | Post daily aggregation summary with day-over-day comparison |
| `notify_connection` | bool | `false` | Alert on connect/disconnect/startup (opt-in) |
| `notify_gap` | bool | `false` | Alert on gap detection and backfill progress (opt-in) |
| `notify_health` | bool | `false` | Alert on node unreachable / channel overflow (opt-in) |

Notifications are async (buffered channel), rate-limited (automatic 429 retry), and only fire from live events (never backfill).

#### `backfill`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `delay` | duration | `100ms` | Delay between block fetches during backfill |
| `progress_interval` | int | `100` | Log progress every N blocks |

#### `logging`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `level` | string | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `format` | string | `json` | Output format: `json` (structured) or `console` (human-readable) |

### Supplier Keys File

The `suppliers.keys_file` option points to a YAML file containing hex-encoded secp256k1 private keys. The monitor derives `pokt1...` bech32 addresses from these keys automatically.

```yaml
# supplier-keys.yaml
keys:
  - "aabbccdd..."    # 64 hex characters (32 bytes)
  - "0xeeff0011..."  # 0x prefix is optional
```

This is the same format used by pocket-relay-miner. Keys are derived using the Cosmos SDK `secp256k1` key type with the `pokt` bech32 prefix.

You can combine both sources:
```yaml
suppliers:
  keys_file: "/path/to/supplier-keys.yaml"
  addresses:
    - "pokt1abc..."  # Additional addresses not in the keys file
```

### Monitor-All vs Filtered Mode

| Aspect | Filtered Mode | Monitor-All Mode |
|--------|--------------|-----------------|
| Config | `addresses` or `keys_file` set | Both empty |
| Events | Only matching suppliers pass through | All settlement events stored |
| Use case | Relay-miner operators | Explorers, PNF, validators |
| Metrics cardinality | Low (your suppliers only) | Higher (all network suppliers) |
| Storage | Moderate | Higher (all events stored) |
| Notifications | Per-supplier relevant events | Network-wide events |

Monitor-all mode is efficient because all events for a block are correlated in memory and written in a single SQLite transaction, regardless of how many suppliers are involved.

## CLI Commands

### `monitor`

Start the live settlement monitoring pipeline.

```bash
# Start from current height (no historical backfill)
pocket-settlement-monitor monitor --config config.yaml

# Start live + backfill from a specific height
pocket-settlement-monitor monitor --config config.yaml --from 500000

# Start live + backfill from a date (resolved via binary search)
pocket-settlement-monitor monitor --config config.yaml --from 2024-02-15
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | required | Path to YAML config file |
| `--from` | | Start backfill from this height (int) or date (YYYY-MM-DD) |

Connects to CometBFT WebSocket, processes settlement events, writes to SQLite, exposes Prometheus metrics, and sends Discord notifications. Detects gaps on reconnect and triggers automatic backfill.

On startup with an empty database and no `--from` flag, monitoring begins from the **current chain height**. When `--from` is set, the monitor starts live immediately **and** backfills from the specified height in parallel — no need for a separate backfill container. If the database already has data, `--from` extends history backwards (deduplication via `INSERT OR IGNORE` handles any overlap).

### `backfill`

Fetch historical settlement events for a block height or date range.

```bash
# By height
pocket-settlement-monitor backfill --config config.yaml --from 60750 --to 60760

# By date (resolved to block heights via binary search against chain timestamps)
pocket-settlement-monitor backfill --config config.yaml --from 2024-02-15 --to 2024-02-20
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | required | Path to YAML config file |
| `--from` | required | Start height (int) or ISO 8601 date (YYYY-MM-DD) |
| `--to` | required | End height (int) or ISO 8601 date (YYYY-MM-DD) |
| `--delay` | `100ms` | Delay between block fetches (overrides config) |

Backfill is safe to re-run (deduplicates via `INSERT OR IGNORE`), does not increment Prometheus counters, and does not trigger Discord notifications. See [`docs/gap-recovery.md`](docs/gap-recovery.md) for deduplication strategy and summary recalculation details.

### `query`

Query settlement data from the local SQLite database.

**Common flags** (available on all subcommands):

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.example.yaml` | Path to config file (for database path) |
| `--supplier` | | Filter by supplier address (`pokt1...`) |
| `--service` | | Filter by service ID |
| `--from` | | Start filter: block height (int) or date (YYYY-MM-DD) |
| `--to` | | End filter: block height (int) or date (YYYY-MM-DD) |
| `--limit` | `0` | Max results (0 = unlimited) |
| `-o, --output` | `table` | Format: `table`, `json`, `csv` |

**Subcommands**:

```bash
# All settlement events (settled, expired, slashed, discarded)
query settlements [--supplier pokt1...] [--service svc] [--output csv]

# Aggregated summaries
query summaries [--period hourly|daily] [--service svc]

# Slashed events only
query slashes [--supplier pokt1...]

# Overservice events
query overservice [--from 2024-02-15 --to 2024-02-16]

# DAO reimbursement requests
query reimbursements [--supplier pokt1...]
```

**Output formats**:
- `table` — Human-readable aligned columns (default)
- `json` — JSON array, one object per row
- `csv` — CSV with header row

### `version`

Print version, commit, and build date.

```bash
pocket-settlement-monitor version
# pocket-settlement-monitor v1.0.0 (abc1234, 2024-02-20T12:00:00Z)
```

## Settlement Event Types

Monitors all 6 poktroll tokenomics settlement events: settled, expired, slashed, discarded, overserviced, and reimbursement requests. See [`docs/settlement-logic.md`](docs/settlement-logic.md) for complete event definitions, proto types, field details, difficulty expansion, and overservice correlation.

## Prometheus Metrics

All metrics use the `psm_` namespace. Counters increment from **live events only** (never backfill). Gauges always update.

**HTTP endpoints**: `/metrics` (Prometheus scrape), `/health` (liveness), `/ready` (readiness — checks WebSocket + DB)

31 metrics across event counters, revenue, relay/CU throughput, settlement latency, operational gauges, notification delivery, and SQLite operations. Label cardinality is configurable via `metrics.labels` in the config.

See [`docs/metrics-reference.md`](docs/metrics-reference.md) for complete metric definitions, labels, example PromQL queries, production alert rules, and Grafana dashboard tips.

## Architecture

Events flow through the pipeline: subscribe -> decode -> collect -> convert -> filter -> persist + record metrics + notify. Overservice is correlated in-memory within the same block (no DB lookups).

See [`docs/architecture.md`](docs/architecture.md) for the full system diagram, component responsibilities, and data flow.

## Development

```bash
make build    # Development build
make test     # Run tests with -race
make lint     # golangci-lint (v2)
make docker   # Build Docker image
make help     # Show all available targets
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full list of Make targets, code standards, testing approach, and PR guidelines.

## Versioning

This project follows [Semantic Versioning](https://semver.org/) with `v` prefix tags:

- **Format**: `vMAJOR.MINOR.PATCH` (e.g., `v1.0.0`, `v1.1.0`, `v2.0.0`)
- **MAJOR**: Breaking changes (config format, database schema, removed CLI flags)
- **MINOR**: New features, new metrics, new CLI commands (backwards compatible)
- **PATCH**: Bug fixes, documentation updates, dependency bumps

Version information is embedded at build time via ldflags into `internal/version/`:

```bash
./bin/pocket-settlement-monitor version
# pocket-settlement-monitor v1.0.0 (abc1234, 2024-02-20T12:00:00Z)
```

When building locally, `git describe --tags --always --dirty` determines the version automatically. Untagged commits show as `v1.0.0-N-gabcdef` (N commits since tag).

## CI/CD

### CI Pipeline

On every push to `main`/`master` and on pull requests (`.github/workflows/ci.yml`):

1. **Lint** — golangci-lint with project config
2. **Test** — `make test` with race detector + coverage report
3. **Build** — Release binary build + version verification + artifact upload
4. **Docker** — Image build + container verification

### Release Pipeline

Triggered by pushing a `v*` tag (`.github/workflows/release.yml`):

1. **Lint** — Full lint check
2. **Test** — Full test suite with race detector
3. **Build** — Cross-platform binaries: linux/darwin x amd64/arm64
4. **Checksums** — SHA256 checksums file (`checksums.txt`)
5. **Docker** — Build and push to GHCR (`:vX.Y.Z` + `:latest`)
6. **Release** — GitHub Release with auto-generated notes and all artifacts

### Creating a Release

```bash
# Ensure you're on main with a clean tree
git checkout main
git pull

# Tag and push
git tag -a v1.1.0 -m "v1.1.0: brief description of what changed"
git push origin v1.1.0
```

Release artifacts:
- `pocket-settlement-monitor-linux-amd64`
- `pocket-settlement-monitor-linux-arm64`
- `pocket-settlement-monitor-darwin-amd64`
- `pocket-settlement-monitor-darwin-arm64`
- `checksums.txt` (SHA256)
- Docker image: `ghcr.io/pokt-network/pocket-settlement-monitor:v1.1.0`

To verify a downloaded binary:

```bash
sha256sum -c checksums.txt
```

## Project Structure

```
main.go                  # Entry point
cmd/                     # CLI commands (monitor, backfill, query, version)
config/                  # Config loading + supplier key derivation
subscriber/              # CometBFT WebSocket client + ABCI event decoder
processor/               # Event collector, processor, filter, reporter
store/                   # SQLite schema, migrations, CRUD, retention
notify/                  # Discord webhook notifications
metrics/                 # Prometheus metrics + HTTP server
logging/                 # zerolog structured logging
internal/version/        # Build version variables (ldflags)
docs/                    # Specification and reference documents
scripts/                 # Testing utilities (mock webhook, integration tests)
```

## Documentation

| Document | Description |
|----------|-------------|
| [`docs/architecture.md`](docs/architecture.md) | System design, component diagram, data flow |
| [`docs/settlement-logic.md`](docs/settlement-logic.md) | All 6 event types, difficulty expansion, overservice correlation |
| [`docs/gap-recovery.md`](docs/gap-recovery.md) | Downtime recovery, parallel live+backfill, deduplication |
| [`docs/metrics-reference.md`](docs/metrics-reference.md) | All Prometheus metrics, alert rules, Grafana tips |
| [`docs/database-schema.md`](docs/database-schema.md) | SQLite schema, tables, indexes, retention, relationships |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Common issues and solutions |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Code standards, testing, PR guidelines |
| [`config.example.yaml`](config.example.yaml) | Annotated example configuration |

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `cometbft` | v1.0.0-rc1 | WebSocket subscription, ABCI types |
| `cosmos-sdk` | v0.53.0 | ParseTypedEvent, secp256k1 crypto |
| `poktroll` | v0.1.31 | Tokenomics event type definitions |
| `prometheus/client_golang` | v1.22.0 | Metrics |
| `modernc.org/sqlite` | v1.46.0 | Pure Go SQLite (no CGO) |
| `cobra` | v1.10.2 | CLI framework |
| `zerolog` | v1.34.0 | Structured logging |
