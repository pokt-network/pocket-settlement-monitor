package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	_ "modernc.org/sqlite"

	"github.com/pokt-network/pocket-settlement-monitor/logging"
)

//go:embed schema.sql
var ddl string

// SQLiteStore implements the Store interface using modernc.org/sqlite (pure Go, no CGO).
type SQLiteStore struct {
	db        *sql.DB
	logger    zerolog.Logger
	retention time.Duration
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	metrics   metricsRecorder
}

// metricsRecorder is the subset of metrics.Metrics needed by the store.
// Using an interface avoids an import cycle between store and metrics.
type metricsRecorder interface {
	RecordSQLiteOperation(operation, status string, isLive bool)
	RecordRetentionRowsDeleted(table string, count float64, isLive bool)
}

// Open creates or opens a SQLite database at the given path and applies the schema.
// PRAGMAs are configured via the DSN: WAL journal mode, busy_timeout=5000ms,
// synchronous=NORMAL, foreign_keys=ON, cache_size=64MB.
// The connection pool is configured as single-writer (MaxOpenConns=1).
// Pass ":memory:" for dbPath to use an in-memory database (useful for tests).
// A retention of 0 means keep data forever.
func Open(ctx context.Context, dbPath string, retention time.Duration, logger zerolog.Logger) (*SQLiteStore, error) {
	storeLogger := logging.ForComponent(logger, "store")

	dsn := buildDSN(dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", dbPath, err)
	}

	// Single-writer connection pool per STOR-01.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // Never expire the connection.

	// Verify the connection is alive.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Apply schema DDL. Idempotent via CREATE TABLE/INDEX IF NOT EXISTS.
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	cleanupCtx, cancel := context.WithCancel(context.Background())

	store := &SQLiteStore{
		db:        db,
		logger:    storeLogger,
		retention: retention,
		cancel:    cancel,
	}

	store.startRetentionCleanup(cleanupCtx)

	storeLogger.Info().
		Str("path", dbPath).
		Dur("retention", retention).
		Msg("database opened")

	return store, nil
}

// buildDSN constructs the SQLite DSN with all required PRAGMAs.
func buildDSN(dbPath string) string {
	pragmas := "_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=cache_size(-64000)"

	if dbPath == ":memory:" {
		return "file::memory:?" + pragmas
	}

	return fmt.Sprintf("file:%s?%s", dbPath, pragmas)
}

// SetMetrics injects a metrics recorder for SQLite operation tracking.
// Call after Open() and before processing begins. Safe to leave unset (nil).
func (s *SQLiteStore) SetMetrics(m metricsRecorder) {
	s.metrics = m
}

// Close releases database resources and cancels any background goroutines.
func (s *SQLiteStore) Close() error {
	s.cancel()
	s.wg.Wait()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}

	s.logger.Info().Msg("database closed")
	return nil
}

// DB returns the underlying *sql.DB for use in tests and advanced queries.
// This is not part of the Store interface.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// InsertBlockEvents atomically inserts all events for a single block in one SQLite
// transaction. Uses INSERT OR IGNORE for deduplication via UNIQUE constraints.
// Reward distributions are linked to their parent settlement via LastInsertId;
// if a settlement INSERT is ignored (duplicate), its reward distributions are skipped.
func (s *SQLiteStore) InsertBlockEvents(ctx context.Context, block ProcessedBlock, settlements []Settlement,
	rewardDists map[int][]RewardDistribution, overservices []OverserviceEvent,
	reimbursements []ReimbursementEvent) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction for block %d: %w", block.Height, err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Prepare statements scoped to this transaction.
	stmtSettlement, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO settlements (
		block_height, block_timestamp, event_type,
		supplier_operator_address, application_address, service_id,
		session_end_block_height, claim_proof_status,
		claimed_upokt, num_relays, num_claimed_compute_units, num_estimated_compute_units,
		proof_requirement, estimated_relays, difficulty_multiplier,
		is_overserviced, effective_burn_upokt, overservice_diff_upokt,
		expiration_reason, error_message, slash_penalty_upokt
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing settlement statement: %w", err)
	}
	defer stmtSettlement.Close()

	stmtRewardDist, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reward_distributions (
		settlement_id, address, amount_upokt
	) VALUES (?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing reward distribution statement: %w", err)
	}
	defer stmtRewardDist.Close()

	stmtOverservice, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO overservice_events (
		block_height, block_timestamp, application_address,
		supplier_operator_address, expected_burn_upokt, effective_burn_upokt
	) VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing overservice statement: %w", err)
	}
	defer stmtOverservice.Close()

	stmtReimbursement, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reimbursement_events (
		block_height, block_timestamp, application_address,
		supplier_operator_address, supplier_owner_address,
		service_id, session_id, amount_upokt
	) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing reimbursement statement: %w", err)
	}
	defer stmtReimbursement.Close()

	blockTS := block.BlockTimestamp.Format(time.RFC3339)

	// Insert settlements and their reward distributions.
	for i, s := range settlements {
		result, err := stmtSettlement.ExecContext(ctx,
			s.BlockHeight, blockTS, s.EventType,
			s.SupplierOperatorAddress, s.ApplicationAddress, s.ServiceID,
			s.SessionEndBlockHeight, s.ClaimProofStatus,
			s.ClaimedUpokt, s.NumRelays, s.NumClaimedComputeUnits, s.NumEstimatedComputeUnits,
			s.ProofRequirement, s.EstimatedRelays, s.DifficultyMultiplier,
			boolToInt(s.IsOverserviced), s.EffectiveBurnUpokt, s.OverserviceDiffUpokt,
			s.ExpirationReason, s.ErrorMessage, s.SlashPenaltyUpokt,
		)
		if err != nil {
			return fmt.Errorf("inserting settlement at height %d: %w", block.Height, err)
		}

		affected, _ := result.RowsAffected()
		if affected > 0 {
			settlementID, _ := result.LastInsertId()
			// Insert reward distributions for this settlement.
			for _, rd := range rewardDists[i] {
				if _, err := stmtRewardDist.ExecContext(ctx, settlementID, rd.Address, rd.AmountUpokt); err != nil {
					return fmt.Errorf("inserting reward distribution at height %d: %w", block.Height, err)
				}
			}
		}
	}

	// Insert overservice events.
	for _, o := range overservices {
		if _, err := stmtOverservice.ExecContext(ctx,
			o.BlockHeight, blockTS,
			o.ApplicationAddress, o.SupplierOperatorAddress,
			o.ExpectedBurnUpokt, o.EffectiveBurnUpokt,
		); err != nil {
			return fmt.Errorf("inserting overservice event at height %d: %w", block.Height, err)
		}
	}

	// Insert reimbursement events.
	for _, r := range reimbursements {
		if _, err := stmtReimbursement.ExecContext(ctx,
			r.BlockHeight, blockTS,
			r.ApplicationAddress, r.SupplierOperatorAddress,
			r.SupplierOwnerAddress, r.ServiceID, r.SessionID,
			r.AmountUpokt,
		); err != nil {
			return fmt.Errorf("inserting reimbursement event at height %d: %w", block.Height, err)
		}
	}

	// Insert processed block record.
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?,?,?,?)`,
		block.Height, blockTS, block.EventCount, block.Source,
	); err != nil {
		return fmt.Errorf("inserting processed block at height %d: %w", block.Height, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing block %d transaction: %w", block.Height, err)
	}

	if s.metrics != nil {
		s.metrics.RecordSQLiteOperation("insert_block_events", "ok", block.Source == "live")
	}

	s.logger.Debug().
		Int64("height", block.Height).
		Int("settlements", len(settlements)).
		Int("overservices", len(overservices)).
		Int("reimbursements", len(reimbursements)).
		Msg("block events inserted")

	return nil
}

// LastProcessedHeight returns the highest block height that has been processed.
// Returns 0 if no blocks have been processed yet (empty database).
func (s *SQLiteStore) LastProcessedHeight(ctx context.Context) (int64, error) {
	var height sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(height) FROM processed_blocks").Scan(&height)
	if err != nil {
		return 0, fmt.Errorf("querying last processed height: %w", err)
	}
	if !height.Valid {
		return 0, nil // Empty database
	}
	return height.Int64, nil
}

// boolToInt converts a boolean to an int (0 or 1) for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpsertHourlySummaryService inserts or updates a per-service hourly summary row.
func (s *SQLiteStore) UpsertHourlySummaryService(ctx context.Context, summary HourlySummaryService) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO hourly_summaries_service (
		hour_start, service_id, claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(hour_start, service_id) DO UPDATE SET
		claims_settled = excluded.claims_settled,
		claims_expired = excluded.claims_expired,
		claims_slashed = excluded.claims_slashed,
		claims_discarded = excluded.claims_discarded,
		claimed_total_upokt = excluded.claimed_total_upokt,
		effective_total_upokt = excluded.effective_total_upokt,
		num_relays = excluded.num_relays,
		estimated_relays = excluded.estimated_relays,
		num_compute_units = excluded.num_compute_units,
		estimated_compute_units = excluded.estimated_compute_units,
		overservice_count = excluded.overservice_count,
		reimbursement_total_upokt = excluded.reimbursement_total_upokt,
		active_supplier_count = excluded.active_supplier_count`,
		summary.HourStart.Format(time.RFC3339), summary.ServiceID,
		summary.ClaimsSettled, summary.ClaimsExpired, summary.ClaimsSlashed, summary.ClaimsDiscarded,
		summary.ClaimedTotalUpokt, summary.EffectiveTotalUpokt,
		summary.NumRelays, summary.EstimatedRelays,
		summary.NumComputeUnits, summary.EstimatedComputeUnits,
		summary.OverserviceCount, summary.ReimbursementTotalUpokt, summary.ActiveSupplierCount,
	)
	if err != nil {
		return fmt.Errorf("upserting hourly service summary: %w", err)
	}
	return nil
}

// UpsertDailySummaryService inserts or updates a per-service daily summary row.
func (s *SQLiteStore) UpsertDailySummaryService(ctx context.Context, summary DailySummaryService) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO daily_summaries_service (
		day_date, service_id, claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(day_date, service_id) DO UPDATE SET
		claims_settled = excluded.claims_settled,
		claims_expired = excluded.claims_expired,
		claims_slashed = excluded.claims_slashed,
		claims_discarded = excluded.claims_discarded,
		claimed_total_upokt = excluded.claimed_total_upokt,
		effective_total_upokt = excluded.effective_total_upokt,
		num_relays = excluded.num_relays,
		estimated_relays = excluded.estimated_relays,
		num_compute_units = excluded.num_compute_units,
		estimated_compute_units = excluded.estimated_compute_units,
		overservice_count = excluded.overservice_count,
		reimbursement_total_upokt = excluded.reimbursement_total_upokt,
		active_supplier_count = excluded.active_supplier_count`,
		summary.DayDate.Format(time.RFC3339), summary.ServiceID,
		summary.ClaimsSettled, summary.ClaimsExpired, summary.ClaimsSlashed, summary.ClaimsDiscarded,
		summary.ClaimedTotalUpokt, summary.EffectiveTotalUpokt,
		summary.NumRelays, summary.EstimatedRelays,
		summary.NumComputeUnits, summary.EstimatedComputeUnits,
		summary.OverserviceCount, summary.ReimbursementTotalUpokt, summary.ActiveSupplierCount,
	)
	if err != nil {
		return fmt.Errorf("upserting daily service summary: %w", err)
	}
	return nil
}

// UpsertHourlySummaryNetwork inserts or updates a network-wide hourly summary row.
func (s *SQLiteStore) UpsertHourlySummaryNetwork(ctx context.Context, summary HourlySummaryNetwork) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO hourly_summaries_network (
		hour_start, claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(hour_start) DO UPDATE SET
		claims_settled = excluded.claims_settled,
		claims_expired = excluded.claims_expired,
		claims_slashed = excluded.claims_slashed,
		claims_discarded = excluded.claims_discarded,
		claimed_total_upokt = excluded.claimed_total_upokt,
		effective_total_upokt = excluded.effective_total_upokt,
		num_relays = excluded.num_relays,
		estimated_relays = excluded.estimated_relays,
		num_compute_units = excluded.num_compute_units,
		estimated_compute_units = excluded.estimated_compute_units,
		overservice_count = excluded.overservice_count,
		reimbursement_total_upokt = excluded.reimbursement_total_upokt,
		active_supplier_count = excluded.active_supplier_count`,
		summary.HourStart.Format(time.RFC3339),
		summary.ClaimsSettled, summary.ClaimsExpired, summary.ClaimsSlashed, summary.ClaimsDiscarded,
		summary.ClaimedTotalUpokt, summary.EffectiveTotalUpokt,
		summary.NumRelays, summary.EstimatedRelays,
		summary.NumComputeUnits, summary.EstimatedComputeUnits,
		summary.OverserviceCount, summary.ReimbursementTotalUpokt, summary.ActiveSupplierCount,
	)
	if err != nil {
		return fmt.Errorf("upserting hourly network summary: %w", err)
	}
	return nil
}

// UpsertDailySummaryNetwork inserts or updates a network-wide daily summary row.
func (s *SQLiteStore) UpsertDailySummaryNetwork(ctx context.Context, summary DailySummaryNetwork) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO daily_summaries_network (
		day_date, claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(day_date) DO UPDATE SET
		claims_settled = excluded.claims_settled,
		claims_expired = excluded.claims_expired,
		claims_slashed = excluded.claims_slashed,
		claims_discarded = excluded.claims_discarded,
		claimed_total_upokt = excluded.claimed_total_upokt,
		effective_total_upokt = excluded.effective_total_upokt,
		num_relays = excluded.num_relays,
		estimated_relays = excluded.estimated_relays,
		num_compute_units = excluded.num_compute_units,
		estimated_compute_units = excluded.estimated_compute_units,
		overservice_count = excluded.overservice_count,
		reimbursement_total_upokt = excluded.reimbursement_total_upokt,
		active_supplier_count = excluded.active_supplier_count`,
		summary.DayDate.Format(time.RFC3339),
		summary.ClaimsSettled, summary.ClaimsExpired, summary.ClaimsSlashed, summary.ClaimsDiscarded,
		summary.ClaimedTotalUpokt, summary.EffectiveTotalUpokt,
		summary.NumRelays, summary.EstimatedRelays,
		summary.NumComputeUnits, summary.EstimatedComputeUnits,
		summary.OverserviceCount, summary.ReimbursementTotalUpokt, summary.ActiveSupplierCount,
	)
	if err != nil {
		return fmt.Errorf("upserting daily network summary: %w", err)
	}
	return nil
}

// RunRetentionCleanup deletes data older than the configured retention period.
// Multipliers: 1x for raw events, 3x for hourly summaries, 6x for daily summaries.
// If retention is 0, no data is deleted (keep forever).
func (s *SQLiteStore) RunRetentionCleanup(ctx context.Context) (RetentionResult, error) {
	if s.retention == 0 {
		s.logger.Info().Msg("retention disabled, skipping cleanup")
		return RetentionResult{}, nil
	}

	now := time.Now()
	rawCutoff := now.Add(-s.retention)
	hourlyCutoff := now.Add(-3 * s.retention)
	dailyCutoff := now.Add(-6 * s.retention)

	rawCutoffStr := rawCutoff.Format(time.RFC3339)
	hourlyCutoffStr := hourlyCutoff.Format(time.RFC3339)
	dailyCutoffStr := dailyCutoff.Format(time.RFC3339)

	result := RetentionResult{
		RawCutoff:    rawCutoff,
		HourlyCutoff: hourlyCutoff,
		DailyCutoff:  dailyCutoff,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("beginning retention cleanup transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete reward_distributions explicitly FIRST for performance (avoids CASCADE row-by-row).
	res, err := tx.ExecContext(ctx,
		`DELETE FROM reward_distributions WHERE settlement_id IN (
			SELECT id FROM settlements WHERE block_timestamp < ?
		)`, rawCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old reward distributions: %w", err)
	}
	result.RewardDistributionsDeleted, _ = res.RowsAffected()

	// Delete old settlements.
	res, err = tx.ExecContext(ctx, `DELETE FROM settlements WHERE block_timestamp < ?`, rawCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old settlements: %w", err)
	}
	result.SettlementsDeleted, _ = res.RowsAffected()

	// Delete old overservice events.
	res, err = tx.ExecContext(ctx, `DELETE FROM overservice_events WHERE block_timestamp < ?`, rawCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old overservice events: %w", err)
	}
	result.OverserviceEventsDeleted, _ = res.RowsAffected()

	// Delete old reimbursement events.
	res, err = tx.ExecContext(ctx, `DELETE FROM reimbursement_events WHERE block_timestamp < ?`, rawCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old reimbursement events: %w", err)
	}
	result.ReimbursementEventsDeleted, _ = res.RowsAffected()

	// Delete old processed blocks.
	res, err = tx.ExecContext(ctx, `DELETE FROM processed_blocks WHERE block_timestamp < ?`, rawCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old processed blocks: %w", err)
	}
	result.ProcessedBlocksDeleted, _ = res.RowsAffected()

	// Delete old hourly summaries (3x retention).
	res, err = tx.ExecContext(ctx, `DELETE FROM hourly_summaries_service WHERE hour_start < ?`, hourlyCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old hourly service summaries: %w", err)
	}
	result.HourlySummaryServiceDeleted, _ = res.RowsAffected()

	res, err = tx.ExecContext(ctx, `DELETE FROM hourly_summaries_network WHERE hour_start < ?`, hourlyCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old hourly network summaries: %w", err)
	}
	result.HourlySummaryNetworkDeleted, _ = res.RowsAffected()

	// Delete old daily summaries (6x retention).
	res, err = tx.ExecContext(ctx, `DELETE FROM daily_summaries_service WHERE day_date < ?`, dailyCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old daily service summaries: %w", err)
	}
	result.DailySummaryServiceDeleted, _ = res.RowsAffected()

	res, err = tx.ExecContext(ctx, `DELETE FROM daily_summaries_network WHERE day_date < ?`, dailyCutoffStr)
	if err != nil {
		return result, fmt.Errorf("deleting old daily network summaries: %w", err)
	}
	result.DailySummaryNetworkDeleted, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("committing retention cleanup transaction: %w", err)
	}

	// Record retention metrics (always isLive=true — retention runs on a live ticker).
	if s.metrics != nil {
		s.metrics.RecordRetentionRowsDeleted("reward_distributions", float64(result.RewardDistributionsDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("settlements", float64(result.SettlementsDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("overservice_events", float64(result.OverserviceEventsDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("reimbursement_events", float64(result.ReimbursementEventsDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("processed_blocks", float64(result.ProcessedBlocksDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("hourly_summaries_service", float64(result.HourlySummaryServiceDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("hourly_summaries_network", float64(result.HourlySummaryNetworkDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("daily_summaries_service", float64(result.DailySummaryServiceDeleted), true)
		s.metrics.RecordRetentionRowsDeleted("daily_summaries_network", float64(result.DailySummaryNetworkDeleted), true)
	}

	s.logger.Info().
		Int64("settlements_deleted", result.SettlementsDeleted).
		Int64("reward_distributions_deleted", result.RewardDistributionsDeleted).
		Int64("overservice_deleted", result.OverserviceEventsDeleted).
		Int64("reimbursement_deleted", result.ReimbursementEventsDeleted).
		Int64("processed_blocks_deleted", result.ProcessedBlocksDeleted).
		Int64("hourly_service_deleted", result.HourlySummaryServiceDeleted).
		Int64("hourly_network_deleted", result.HourlySummaryNetworkDeleted).
		Int64("daily_service_deleted", result.DailySummaryServiceDeleted).
		Int64("daily_network_deleted", result.DailySummaryNetworkDeleted).
		Time("raw_cutoff", result.RawCutoff).
		Time("hourly_cutoff", result.HourlyCutoff).
		Time("daily_cutoff", result.DailyCutoff).
		Msg("retention cleanup completed")

	return result, nil
}

// GetHourlySummaryService returns the per-service hourly summary for the given hour and service.
// Returns a zero-value struct if no row exists (not an error).
func (s *SQLiteStore) GetHourlySummaryService(ctx context.Context, hourStart time.Time, serviceID string) (HourlySummaryService, error) {
	var summary HourlySummaryService
	var hourStartStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, hour_start, service_id,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM hourly_summaries_service WHERE hour_start = ? AND service_id = ?`,
		hourStart.Format(time.RFC3339), serviceID,
	).Scan(
		&summary.ID, &hourStartStr, &summary.ServiceID,
		&summary.ClaimsSettled, &summary.ClaimsExpired, &summary.ClaimsSlashed, &summary.ClaimsDiscarded,
		&summary.ClaimedTotalUpokt, &summary.EffectiveTotalUpokt,
		&summary.NumRelays, &summary.EstimatedRelays,
		&summary.NumComputeUnits, &summary.EstimatedComputeUnits,
		&summary.OverserviceCount, &summary.ReimbursementTotalUpokt, &summary.ActiveSupplierCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HourlySummaryService{}, nil
	}
	if err != nil {
		return HourlySummaryService{}, fmt.Errorf("getting hourly service summary: %w", err)
	}
	ts, err := time.Parse(time.RFC3339, hourStartStr)
	if err != nil {
		return HourlySummaryService{}, fmt.Errorf("parsing hour_start %q: %w", hourStartStr, err)
	}
	summary.HourStart = ts
	return summary, nil
}

// GetHourlySummaryNetwork returns the network-wide hourly summary for the given hour.
// Returns a zero-value struct if no row exists (not an error).
func (s *SQLiteStore) GetHourlySummaryNetwork(ctx context.Context, hourStart time.Time) (HourlySummaryNetwork, error) {
	var summary HourlySummaryNetwork
	var hourStartStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, hour_start,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM hourly_summaries_network WHERE hour_start = ?`,
		hourStart.Format(time.RFC3339),
	).Scan(
		&summary.ID, &hourStartStr,
		&summary.ClaimsSettled, &summary.ClaimsExpired, &summary.ClaimsSlashed, &summary.ClaimsDiscarded,
		&summary.ClaimedTotalUpokt, &summary.EffectiveTotalUpokt,
		&summary.NumRelays, &summary.EstimatedRelays,
		&summary.NumComputeUnits, &summary.EstimatedComputeUnits,
		&summary.OverserviceCount, &summary.ReimbursementTotalUpokt, &summary.ActiveSupplierCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HourlySummaryNetwork{}, nil
	}
	if err != nil {
		return HourlySummaryNetwork{}, fmt.Errorf("getting hourly network summary: %w", err)
	}
	ts, err := time.Parse(time.RFC3339, hourStartStr)
	if err != nil {
		return HourlySummaryNetwork{}, fmt.Errorf("parsing hour_start %q: %w", hourStartStr, err)
	}
	summary.HourStart = ts
	return summary, nil
}

// GetDailySummaryService returns the per-service daily summary for the given date and service.
// Returns a zero-value struct if no row exists (not an error).
func (s *SQLiteStore) GetDailySummaryService(ctx context.Context, dayDate time.Time, serviceID string) (DailySummaryService, error) {
	var summary DailySummaryService
	var dayDateStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, day_date, service_id,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM daily_summaries_service WHERE day_date = ? AND service_id = ?`,
		dayDate.Format(time.RFC3339), serviceID,
	).Scan(
		&summary.ID, &dayDateStr, &summary.ServiceID,
		&summary.ClaimsSettled, &summary.ClaimsExpired, &summary.ClaimsSlashed, &summary.ClaimsDiscarded,
		&summary.ClaimedTotalUpokt, &summary.EffectiveTotalUpokt,
		&summary.NumRelays, &summary.EstimatedRelays,
		&summary.NumComputeUnits, &summary.EstimatedComputeUnits,
		&summary.OverserviceCount, &summary.ReimbursementTotalUpokt, &summary.ActiveSupplierCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DailySummaryService{}, nil
	}
	if err != nil {
		return DailySummaryService{}, fmt.Errorf("getting daily service summary: %w", err)
	}
	ts, err := time.Parse(time.RFC3339, dayDateStr)
	if err != nil {
		return DailySummaryService{}, fmt.Errorf("parsing day_date %q: %w", dayDateStr, err)
	}
	summary.DayDate = ts
	return summary, nil
}

// GetDailySummaryNetwork returns the network-wide daily summary for the given date.
// Returns a zero-value struct if no row exists (not an error).
func (s *SQLiteStore) GetDailySummaryNetwork(ctx context.Context, dayDate time.Time) (DailySummaryNetwork, error) {
	var summary DailySummaryNetwork
	var dayDateStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, day_date,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM daily_summaries_network WHERE day_date = ?`,
		dayDate.Format(time.RFC3339),
	).Scan(
		&summary.ID, &dayDateStr,
		&summary.ClaimsSettled, &summary.ClaimsExpired, &summary.ClaimsSlashed, &summary.ClaimsDiscarded,
		&summary.ClaimedTotalUpokt, &summary.EffectiveTotalUpokt,
		&summary.NumRelays, &summary.EstimatedRelays,
		&summary.NumComputeUnits, &summary.EstimatedComputeUnits,
		&summary.OverserviceCount, &summary.ReimbursementTotalUpokt, &summary.ActiveSupplierCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DailySummaryNetwork{}, nil
	}
	if err != nil {
		return DailySummaryNetwork{}, fmt.Errorf("getting daily network summary: %w", err)
	}
	ts, err := time.Parse(time.RFC3339, dayDateStr)
	if err != nil {
		return DailySummaryNetwork{}, fmt.Errorf("parsing day_date %q: %w", dayDateStr, err)
	}
	summary.DayDate = ts
	return summary, nil
}

// DistinctServiceIDs returns all unique service IDs from the settlements table,
// sorted alphabetically. Returns an empty slice on an empty table (not an error).
func (s *SQLiteStore) DistinctServiceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT service_id FROM settlements ORDER BY service_id`)
	if err != nil {
		return nil, fmt.Errorf("querying distinct service IDs: %w", err)
	}
	defer rows.Close()

	var serviceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning service ID: %w", err)
		}
		serviceIDs = append(serviceIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating service IDs: %w", err)
	}

	if serviceIDs == nil {
		serviceIDs = []string{}
	}
	return serviceIDs, nil
}

// CountActiveSuppliers returns the count of distinct supplier operator addresses
// in the settlements table within the given time range. When serviceID is non-empty,
// only suppliers for that service are counted.
func (s *SQLiteStore) CountActiveSuppliers(ctx context.Context, from, to time.Time, serviceID string) (int64, error) {
	fromStr := from.Format(time.RFC3339)
	toStr := to.Format(time.RFC3339)

	var count int64
	if serviceID != "" {
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT supplier_operator_address) FROM settlements
			WHERE block_timestamp >= ? AND block_timestamp < ? AND service_id = ?`,
			fromStr, toStr, serviceID,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("counting active suppliers for service %s: %w", serviceID, err)
		}
	} else {
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT supplier_operator_address) FROM settlements
			WHERE block_timestamp >= ? AND block_timestamp < ?`,
			fromStr, toStr,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("counting active suppliers: %w", err)
		}
	}

	return count, nil
}

// QuerySettlementsForPeriod returns all settlements within the given time range,
// ordered by block_height and id.
func (s *SQLiteStore) QuerySettlementsForPeriod(ctx context.Context, from, to time.Time) ([]Settlement, error) {
	fromStr := from.Format(time.RFC3339)
	toStr := to.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `SELECT id, block_height, block_timestamp, event_type,
		supplier_operator_address, application_address, service_id,
		session_end_block_height, claim_proof_status,
		claimed_upokt, num_relays, num_claimed_compute_units, num_estimated_compute_units,
		proof_requirement, estimated_relays, difficulty_multiplier,
		is_overserviced, effective_burn_upokt, overservice_diff_upokt,
		expiration_reason, error_message, slash_penalty_upokt
		FROM settlements WHERE block_timestamp >= ? AND block_timestamp < ?
		ORDER BY block_height, id`, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("querying settlements for period: %w", err)
	}
	defer rows.Close()

	var settlements []Settlement
	for rows.Next() {
		var s Settlement
		var blockTSStr string
		var isOverserviced int
		if err := rows.Scan(
			&s.ID, &s.BlockHeight, &blockTSStr, &s.EventType,
			&s.SupplierOperatorAddress, &s.ApplicationAddress, &s.ServiceID,
			&s.SessionEndBlockHeight, &s.ClaimProofStatus,
			&s.ClaimedUpokt, &s.NumRelays, &s.NumClaimedComputeUnits, &s.NumEstimatedComputeUnits,
			&s.ProofRequirement, &s.EstimatedRelays, &s.DifficultyMultiplier,
			&isOverserviced, &s.EffectiveBurnUpokt, &s.OverserviceDiffUpokt,
			&s.ExpirationReason, &s.ErrorMessage, &s.SlashPenaltyUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning settlement row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing settlement block_timestamp %q: %w", blockTSStr, err)
		}
		s.BlockTimestamp = ts
		s.IsOverserviced = isOverserviced != 0
		settlements = append(settlements, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating settlements: %w", err)
	}

	return settlements, nil
}

// QueryOverserviceEventsForPeriod returns all overservice events within the given time range.
func (s *SQLiteStore) QueryOverserviceEventsForPeriod(ctx context.Context, from, to time.Time) ([]OverserviceEvent, error) {
	fromStr := from.Format(time.RFC3339)
	toStr := to.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `SELECT id, block_height, block_timestamp,
		application_address, supplier_operator_address,
		expected_burn_upokt, effective_burn_upokt
		FROM overservice_events WHERE block_timestamp >= ? AND block_timestamp < ?`,
		fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("querying overservice events for period: %w", err)
	}
	defer rows.Close()

	var events []OverserviceEvent
	for rows.Next() {
		var e OverserviceEvent
		var blockTSStr string
		if err := rows.Scan(
			&e.ID, &e.BlockHeight, &blockTSStr,
			&e.ApplicationAddress, &e.SupplierOperatorAddress,
			&e.ExpectedBurnUpokt, &e.EffectiveBurnUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning overservice event row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing overservice block_timestamp %q: %w", blockTSStr, err)
		}
		e.BlockTimestamp = ts
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating overservice events: %w", err)
	}

	return events, nil
}

// QueryReimbursementEventsForPeriod returns all reimbursement events within the given time range.
func (s *SQLiteStore) QueryReimbursementEventsForPeriod(ctx context.Context, from, to time.Time) ([]ReimbursementEvent, error) {
	fromStr := from.Format(time.RFC3339)
	toStr := to.Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx, `SELECT id, block_height, block_timestamp,
		application_address, supplier_operator_address, supplier_owner_address,
		service_id, session_id, amount_upokt
		FROM reimbursement_events WHERE block_timestamp >= ? AND block_timestamp < ?`,
		fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("querying reimbursement events for period: %w", err)
	}
	defer rows.Close()

	var events []ReimbursementEvent
	for rows.Next() {
		var e ReimbursementEvent
		var blockTSStr string
		if err := rows.Scan(
			&e.ID, &e.BlockHeight, &blockTSStr,
			&e.ApplicationAddress, &e.SupplierOperatorAddress, &e.SupplierOwnerAddress,
			&e.ServiceID, &e.SessionID, &e.AmountUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning reimbursement event row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing reimbursement block_timestamp %q: %w", blockTSStr, err)
		}
		e.BlockTimestamp = ts
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating reimbursement events: %w", err)
	}

	return events, nil
}

// startRetentionCleanup starts a background goroutine that runs retention cleanup hourly.
// If retention is 0, no goroutine is started.
func (s *SQLiteStore) startRetentionCleanup(ctx context.Context) {
	if s.retention == 0 {
		s.logger.Info().Msg("retention disabled (0), no cleanup goroutine started")
		return
	}

	ticker := time.NewTicker(1 * time.Hour)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.RunRetentionCleanup(ctx); err != nil {
					s.logger.Error().Err(err).Msg("retention cleanup failed")
				}
			}
		}
	}()

	s.logger.Info().
		Dur("retention", s.retention).
		Msg("retention cleanup scheduled")
}
