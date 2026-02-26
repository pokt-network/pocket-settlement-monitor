-- pocket-settlement-monitor schema v1
-- All statements use IF NOT EXISTS for idempotent migration on every Open().

-- settlements: single table for all 4 claim event types (settled, expired, slashed, discarded)
CREATE TABLE IF NOT EXISTS settlements (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    block_height INTEGER NOT NULL,
    block_timestamp TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK(event_type IN ('settled','expired','slashed','discarded')),

    -- Common fields (all event types)
    supplier_operator_address TEXT NOT NULL,
    application_address TEXT NOT NULL,
    service_id TEXT NOT NULL DEFAULT '',
    session_end_block_height INTEGER NOT NULL,
    claim_proof_status INTEGER NOT NULL DEFAULT 0,

    -- Settled/Expired fields
    claimed_upokt INTEGER NOT NULL DEFAULT 0,
    num_relays INTEGER NOT NULL DEFAULT 0,
    num_claimed_compute_units INTEGER NOT NULL DEFAULT 0,
    num_estimated_compute_units INTEGER NOT NULL DEFAULT 0,
    proof_requirement INTEGER NOT NULL DEFAULT 0,

    -- Computed fields (populated by processor)
    estimated_relays INTEGER NOT NULL DEFAULT 0,
    difficulty_multiplier REAL NOT NULL DEFAULT 1.0,

    -- Overservice correlation (populated by processor from same-block overservice events)
    is_overserviced INTEGER NOT NULL DEFAULT 0,
    effective_burn_upokt INTEGER NOT NULL DEFAULT 0,
    overservice_diff_upokt INTEGER NOT NULL DEFAULT 0,

    -- Type-specific fields
    expiration_reason TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    slash_penalty_upokt INTEGER NOT NULL DEFAULT 0,

    UNIQUE(block_height, event_type, supplier_operator_address, application_address, session_end_block_height)
);

-- Normalized reward distributions (settled events only)
CREATE TABLE IF NOT EXISTS reward_distributions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    settlement_id INTEGER NOT NULL REFERENCES settlements(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    amount_upokt INTEGER NOT NULL,
    UNIQUE(settlement_id, address)
);

-- Overservice events (raw, before correlation)
CREATE TABLE IF NOT EXISTS overservice_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    block_height INTEGER NOT NULL,
    block_timestamp TEXT NOT NULL,
    application_address TEXT NOT NULL,
    supplier_operator_address TEXT NOT NULL,
    expected_burn_upokt INTEGER NOT NULL,
    effective_burn_upokt INTEGER NOT NULL,
    UNIQUE(block_height, application_address, supplier_operator_address)
);

-- Reimbursement events
CREATE TABLE IF NOT EXISTS reimbursement_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    block_height INTEGER NOT NULL,
    block_timestamp TEXT NOT NULL,
    application_address TEXT NOT NULL,
    supplier_operator_address TEXT NOT NULL,
    supplier_owner_address TEXT NOT NULL,
    service_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    amount_upokt INTEGER NOT NULL,
    UNIQUE(block_height, application_address, supplier_operator_address, session_id)
);

-- Processed blocks tracker
CREATE TABLE IF NOT EXISTS processed_blocks (
    height INTEGER PRIMARY KEY,
    block_timestamp TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL CHECK(source IN ('live','backfill'))
);

-- Per-service hourly summaries
CREATE TABLE IF NOT EXISTS hourly_summaries_service (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hour_start TEXT NOT NULL,
    service_id TEXT NOT NULL,
    claims_settled INTEGER NOT NULL DEFAULT 0,
    claims_expired INTEGER NOT NULL DEFAULT 0,
    claims_slashed INTEGER NOT NULL DEFAULT 0,
    claims_discarded INTEGER NOT NULL DEFAULT 0,
    claimed_total_upokt INTEGER NOT NULL DEFAULT 0,
    effective_total_upokt INTEGER NOT NULL DEFAULT 0,
    num_relays INTEGER NOT NULL DEFAULT 0,
    estimated_relays INTEGER NOT NULL DEFAULT 0,
    num_compute_units INTEGER NOT NULL DEFAULT 0,
    estimated_compute_units INTEGER NOT NULL DEFAULT 0,
    overservice_count INTEGER NOT NULL DEFAULT 0,
    reimbursement_total_upokt INTEGER NOT NULL DEFAULT 0,
    active_supplier_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(hour_start, service_id)
);

-- Network-wide hourly summaries (separate table per user decision)
CREATE TABLE IF NOT EXISTS hourly_summaries_network (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hour_start TEXT NOT NULL UNIQUE,
    claims_settled INTEGER NOT NULL DEFAULT 0,
    claims_expired INTEGER NOT NULL DEFAULT 0,
    claims_slashed INTEGER NOT NULL DEFAULT 0,
    claims_discarded INTEGER NOT NULL DEFAULT 0,
    claimed_total_upokt INTEGER NOT NULL DEFAULT 0,
    effective_total_upokt INTEGER NOT NULL DEFAULT 0,
    num_relays INTEGER NOT NULL DEFAULT 0,
    estimated_relays INTEGER NOT NULL DEFAULT 0,
    num_compute_units INTEGER NOT NULL DEFAULT 0,
    estimated_compute_units INTEGER NOT NULL DEFAULT 0,
    overservice_count INTEGER NOT NULL DEFAULT 0,
    reimbursement_total_upokt INTEGER NOT NULL DEFAULT 0,
    active_supplier_count INTEGER NOT NULL DEFAULT 0
);

-- Per-service daily summaries
CREATE TABLE IF NOT EXISTS daily_summaries_service (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    day_date TEXT NOT NULL,
    service_id TEXT NOT NULL,
    claims_settled INTEGER NOT NULL DEFAULT 0,
    claims_expired INTEGER NOT NULL DEFAULT 0,
    claims_slashed INTEGER NOT NULL DEFAULT 0,
    claims_discarded INTEGER NOT NULL DEFAULT 0,
    claimed_total_upokt INTEGER NOT NULL DEFAULT 0,
    effective_total_upokt INTEGER NOT NULL DEFAULT 0,
    num_relays INTEGER NOT NULL DEFAULT 0,
    estimated_relays INTEGER NOT NULL DEFAULT 0,
    num_compute_units INTEGER NOT NULL DEFAULT 0,
    estimated_compute_units INTEGER NOT NULL DEFAULT 0,
    overservice_count INTEGER NOT NULL DEFAULT 0,
    reimbursement_total_upokt INTEGER NOT NULL DEFAULT 0,
    active_supplier_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(day_date, service_id)
);

-- Network-wide daily summaries (separate table per user decision)
CREATE TABLE IF NOT EXISTS daily_summaries_network (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    day_date TEXT NOT NULL UNIQUE,
    claims_settled INTEGER NOT NULL DEFAULT 0,
    claims_expired INTEGER NOT NULL DEFAULT 0,
    claims_slashed INTEGER NOT NULL DEFAULT 0,
    claims_discarded INTEGER NOT NULL DEFAULT 0,
    claimed_total_upokt INTEGER NOT NULL DEFAULT 0,
    effective_total_upokt INTEGER NOT NULL DEFAULT 0,
    num_relays INTEGER NOT NULL DEFAULT 0,
    estimated_relays INTEGER NOT NULL DEFAULT 0,
    num_compute_units INTEGER NOT NULL DEFAULT 0,
    estimated_compute_units INTEGER NOT NULL DEFAULT 0,
    overservice_count INTEGER NOT NULL DEFAULT 0,
    reimbursement_total_upokt INTEGER NOT NULL DEFAULT 0,
    active_supplier_count INTEGER NOT NULL DEFAULT 0
);

-- Schema version tracking for future migrations
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT OR IGNORE INTO schema_version (version) VALUES (1);

-- Indexes: common query patterns

-- Settlements
CREATE INDEX IF NOT EXISTS idx_settlements_supplier ON settlements(supplier_operator_address);
CREATE INDEX IF NOT EXISTS idx_settlements_service ON settlements(service_id);
CREATE INDEX IF NOT EXISTS idx_settlements_block_ts ON settlements(block_timestamp);
CREATE INDEX IF NOT EXISTS idx_settlements_height ON settlements(block_height);
CREATE INDEX IF NOT EXISTS idx_settlements_session ON settlements(supplier_operator_address, application_address, session_end_block_height);

-- Reward distributions
CREATE INDEX IF NOT EXISTS idx_reward_dist_address ON reward_distributions(address);

-- Overservice events
CREATE INDEX IF NOT EXISTS idx_overservice_block_ts ON overservice_events(block_timestamp);

-- Reimbursement events
CREATE INDEX IF NOT EXISTS idx_reimbursement_block_ts ON reimbursement_events(block_timestamp);
CREATE INDEX IF NOT EXISTS idx_reimbursement_link ON reimbursement_events(supplier_operator_address, application_address, block_height);
