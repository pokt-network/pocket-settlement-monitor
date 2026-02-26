# Database Schema

pocket-settlement-monitor uses SQLite (via `modernc.org/sqlite`, pure Go) with a single-writer connection pool. The schema is applied idempotently on every startup via `CREATE TABLE/INDEX IF NOT EXISTS`.

Schema version: **v1** (tracked in `schema_version` table).

## Tables

### `settlements`

Primary table for all 4 claim-related event types: settled, expired, slashed, discarded.

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment row ID |
| `block_height` | INTEGER | Block where the event was emitted |
| `block_timestamp` | TEXT | ISO 8601 timestamp of the block |
| `event_type` | TEXT | One of: `settled`, `expired`, `slashed`, `discarded` |
| `supplier_operator_address` | TEXT | Supplier's `pokt1...` operator address |
| `application_address` | TEXT | Application's `pokt1...` address |
| `service_id` | TEXT | Service identifier (e.g., `pnf-anvil`) |
| `session_end_block_height` | INTEGER | Height at which the session ended |
| `claim_proof_status` | INTEGER | 0=CLAIMED, 1=PROVEN, 2=SETTLED, 3=EXPIRED |
| `claimed_upokt` | INTEGER | Amount of uPOKT claimed |
| `num_relays` | INTEGER | Relays in merkle tree (passed difficulty) |
| `num_claimed_compute_units` | INTEGER | CUs from merkle tree (before expansion) |
| `num_estimated_compute_units` | INTEGER | CUs after difficulty multiplier |
| `proof_requirement` | INTEGER | 0=NOT_REQUIRED, 1=REQUIRED |
| `estimated_relays` | INTEGER | Difficulty-expanded relay count (computed) |
| `difficulty_multiplier` | REAL | Expansion factor (computed) |
| `is_overserviced` | INTEGER | 1 if correlated with an overservice event |
| `effective_burn_upokt` | INTEGER | Actual burn when overserviced |
| `overservice_diff_upokt` | INTEGER | ExpectedBurn - EffectiveBurn |
| `expiration_reason` | TEXT | Reason for expiration (expired events only) |
| `error_message` | TEXT | Error details (discarded events only) |
| `slash_penalty_upokt` | INTEGER | Penalty amount (slashed events only) |

**Unique constraint**: `(block_height, event_type, supplier_operator_address, application_address, session_end_block_height)`

This constraint enables `INSERT OR IGNORE` deduplication between live and backfill paths.

### `reward_distributions`

Normalized reward distributions for settled events. Each settlement can distribute rewards to multiple addresses (supplier operator, owner, DAO, etc.).

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment row ID |
| `settlement_id` | INTEGER FK | References `settlements(id)` with `ON DELETE CASCADE` |
| `address` | TEXT | Recipient `pokt1...` address |
| `amount_upokt` | INTEGER | Amount distributed |

**Unique constraint**: `(settlement_id, address)`

### `overservice_events`

Raw overservice events before correlation with settlements.

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment row ID |
| `block_height` | INTEGER | Block where the event was emitted |
| `block_timestamp` | TEXT | ISO 8601 timestamp |
| `application_address` | TEXT | Overserviced application |
| `supplier_operator_address` | TEXT | Supplier involved |
| `expected_burn_upokt` | INTEGER | What should have been burned |
| `effective_burn_upokt` | INTEGER | What was actually burned |

**Unique constraint**: `(block_height, application_address, supplier_operator_address)`

### `reimbursement_events`

DAO reimbursement requests for overserviced applications.

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment row ID |
| `block_height` | INTEGER | Block where the event was emitted |
| `block_timestamp` | TEXT | ISO 8601 timestamp |
| `application_address` | TEXT | Application requesting reimbursement |
| `supplier_operator_address` | TEXT | Supplier's operator address |
| `supplier_owner_address` | TEXT | Supplier's owner address |
| `service_id` | TEXT | Service identifier |
| `session_id` | TEXT | Session identifier |
| `amount_upokt` | INTEGER | Reimbursement amount |

**Unique constraint**: `(block_height, application_address, supplier_operator_address, session_id)`

### `processed_blocks`

Tracks which block heights have been processed, used for gap detection.

| Column | Type | Description |
|--------|------|-------------|
| `height` | INTEGER PK | Block height (primary key, no duplicates) |
| `block_timestamp` | TEXT | ISO 8601 timestamp |
| `event_count` | INTEGER | Number of settlement events in this block |
| `source` | TEXT | `live` or `backfill` |

### Summary Tables

Four summary tables store pre-aggregated data at hourly and daily granularity, split by per-service and network-wide:

- **`hourly_summaries_service`** — Per-service hourly aggregation. Unique on `(hour_start, service_id)`.
- **`hourly_summaries_network`** — Network-wide hourly aggregation. Unique on `(hour_start)`.
- **`daily_summaries_service`** — Per-service daily aggregation. Unique on `(day_date, service_id)`.
- **`daily_summaries_network`** — Network-wide daily aggregation. Unique on `(day_date)`.

All four share the same columns:

| Column | Type | Description |
|--------|------|-------------|
| `claims_settled` | INTEGER | Count of settled claims |
| `claims_expired` | INTEGER | Count of expired claims |
| `claims_slashed` | INTEGER | Count of slashed claims |
| `claims_discarded` | INTEGER | Count of discarded claims |
| `claimed_total_upokt` | INTEGER | Total uPOKT claimed |
| `effective_total_upokt` | INTEGER | Total uPOKT after overservice adjustments |
| `num_relays` | INTEGER | Merkle tree relays |
| `estimated_relays` | INTEGER | Difficulty-expanded relays |
| `num_compute_units` | INTEGER | Merkle tree compute units |
| `estimated_compute_units` | INTEGER | Expanded compute units |
| `overservice_count` | INTEGER | Number of overservice events |
| `reimbursement_total_upokt` | INTEGER | Total reimbursement amount |
| `active_supplier_count` | INTEGER | Distinct suppliers active in the period |

Summaries are recalculated when backfill inserts data for past periods (see [Gap Recovery](gap-recovery.md#summary-recalculation-after-backfill)).

### `schema_version`

Tracks the current schema version for future migrations.

| Column | Type | Description |
|--------|------|-------------|
| `version` | INTEGER PK | Schema version number |
| `applied_at` | TEXT | Timestamp when applied |

## Indexes

| Index | Table | Columns | Purpose |
|-------|-------|---------|---------|
| `idx_settlements_supplier` | settlements | `supplier_operator_address` | Filter by supplier |
| `idx_settlements_service` | settlements | `service_id` | Filter by service |
| `idx_settlements_block_ts` | settlements | `block_timestamp` | Date range queries |
| `idx_settlements_height` | settlements | `block_height` | Height range queries |
| `idx_settlements_session` | settlements | `supplier_operator_address, application_address, session_end_block_height` | Session lookups |
| `idx_reward_dist_address` | reward_distributions | `address` | Reward recipient queries |
| `idx_overservice_block_ts` | overservice_events | `block_timestamp` | Date range queries |
| `idx_reimbursement_block_ts` | reimbursement_events | `block_timestamp` | Date range queries |
| `idx_reimbursement_link` | reimbursement_events | `supplier_operator_address, application_address, block_height` | Correlation lookups |

## Deduplication

All data tables use `INSERT OR IGNORE` with UNIQUE constraints. When both live WebSocket and backfill write the same event, whichever arrives first wins; the second is silently skipped. This makes concurrent live + backfill safe without locks.

## Retention

Configurable via `database.retention` (default: `360h` / 15 days, `0` = keep forever).

A background goroutine runs hourly and deletes rows where `block_timestamp` is older than the retention window. Summary tables use extended retention:
- Hourly summaries: 3x the base retention
- Daily summaries: 6x the base retention

Deleted row counts are tracked via the `psm_retention_rows_deleted_total` Prometheus counter.

## SQLite Configuration

Connection settings applied via DSN pragmas:
- **Journal mode**: WAL (write-ahead logging) for concurrent reads during writes
- **Busy timeout**: 5000ms
- **Synchronous**: NORMAL (safe with WAL)
- **Foreign keys**: ON
- **Cache size**: 64MB
- **Connection pool**: Single writer (`MaxOpenConns=1`, `MaxIdleConns=1`)

## Entity Relationships

```
settlements 1───N reward_distributions  (FK: settlement_id → settlements.id, CASCADE)

settlements ←──── overservice_events    (correlated in-memory by supplier+app within same block)

settlements ←──── reimbursement_events  (correlated by supplier+app+block)

processed_blocks  (independent tracker, one row per processed block height)

hourly/daily summaries  (materialized aggregates from settlements + overservice + reimbursements)
```
