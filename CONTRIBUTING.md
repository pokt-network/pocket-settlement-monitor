# Contributing to pocket-settlement-monitor

## Prerequisites

- Go 1.25+
- [golangci-lint](https://golangci-lint.run/) v2 (config requires `version: "2"`)
- Docker (optional, for image builds)

## Development Workflow

```bash
# Clone and build
git clone https://github.com/pokt-network/pocket-settlement-monitor.git
cd pocket-settlement-monitor
make build

# Run the full check suite
make fmt lint test

# Run tests with race detector
make test

# Run tests with coverage
make test-coverage
```

## Code Standards

### Error Handling

Always check errors and wrap with context:

```go
result, err := doSomething()
if err != nil {
    return fmt.Errorf("doing something: %w", err)
}
```

### Logging

Use zerolog structured logging with `ForComponent` for sub-loggers:

```go
logger := logging.ForComponent(parentLogger, "mycomponent")
logger.Info().Str("key", "value").Msg("something happened")
```

Log levels:
- **error**: Something failed and needs attention
- **warn**: Degraded operation (e.g., notification dropped, rate limited)
- **info**: Normal operational events (connected, block processed, backfill complete)
- **debug**: Diagnostic detail (unexpected event types, skipped events)

### Testing

- **No mocks**. Use real implementations with in-memory SQLite (`:memory:`) for database tests.
- All tests run with `-race` flag.
- Use `t.Helper()` in test helpers.
- Test files live alongside the code they test (e.g., `processor/processor_test.go`).

Example test setup with in-memory SQLite:

```go
func TestMyFeature(t *testing.T) {
    ctx := context.Background()
    logger := zerolog.Nop()
    store, err := store.Open(ctx, ":memory:", 0, logger)
    require.NoError(t, err)
    defer store.Close()
    // ... test using real store
}
```

### No CGO

This project uses `modernc.org/sqlite` (pure Go SQLite). Never add CGO dependencies. Production builds use `CGO_ENABLED=0`.

### Import Organization

Imports are organized in three groups separated by blank lines:

1. Standard library
2. Third-party packages
3. Local packages (`github.com/pokt-network/pocket-settlement-monitor/...`)

This is enforced by `goimports` with `-local` flag (run `make fmt`).

## Linting

The project uses golangci-lint v2 with the config in `.golangci.yml`. Key linters enabled:

- `bodyclose` — HTTP response body leaks
- `errorlint` — Correct error wrapping patterns
- `misspell` — Spelling (US English)
- `nilerr` — Returning nil when err is non-nil
- `unconvert` — Unnecessary type conversions
- `govet` — Go vet with all analyzers (except `fieldalignment`)

Run locally:

```bash
make lint
```

## Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Development build |
| `make build-release` | Production build (stripped, CGO_ENABLED=0) |
| `make test` | Run all tests with `-race` |
| `make test-coverage` | Generate coverage report |
| `make fmt` | Format with gofmt + goimports |
| `make lint` | Run golangci-lint |
| `make tidy` | Run go mod tidy |
| `make clean` | Remove build artifacts |
| `make docker` | Build Docker image |
| `make mock-webhook` | Start mock Discord webhook server |
| `make test-beta` | Integration tests against beta testnet |
| `make test-mainnet` | Integration tests against mainnet |
| `make help` | Show all available targets |

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
scripts/                 # Testing and utility scripts
```

## Pull Request Guidelines

1. **Branch from `main`** and open PRs against `main`.
2. **Run `make fmt lint test`** before pushing. CI will catch it, but faster locally.
3. **Keep PRs focused**: one feature, one bug fix, or one refactor per PR.
4. **Write tests** for new functionality.
5. **Update docs** if you change CLI flags, config options, metrics, or behavior.

## Key Documentation

Read these before making changes to core logic:

- [`docs/settlement-logic.md`](docs/settlement-logic.md) — All 6 event types, difficulty expansion, overservice correlation
- [`docs/architecture.md`](docs/architecture.md) — System design and data flow
- [`docs/gap-recovery.md`](docs/gap-recovery.md) — Deduplication, backfill, Prometheus rules
- [`docs/metrics-reference.md`](docs/metrics-reference.md) — All Prometheus metrics
- [`docs/database-schema.md`](docs/database-schema.md) — SQLite schema and relationships

## CI/CD

CI runs on every push and PR to `main`:
- Lint (golangci-lint)
- Test (with race detector + coverage)
- Build (release binary)
- Docker (build + verify)

Releases are triggered by pushing a `v*` tag. See `.github/workflows/release.yml`.
