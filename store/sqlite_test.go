package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// newTestStore creates an in-memory SQLiteStore for testing.
// The store is automatically closed when the test completes.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	store, err := Open(context.Background(), ":memory:", 0, zerolog.Nop())
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}

func TestOpen_CreatesAllTables(t *testing.T) {
	store := newTestStore(t)

	expectedTables := []string{
		"settlements",
		"reward_distributions",
		"overservice_events",
		"reimbursement_events",
		"processed_blocks",
		"hourly_summaries_service",
		"hourly_summaries_network",
		"daily_summaries_service",
		"daily_summaries_network",
		"schema_version",
	}

	rows, err := store.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	require.NoError(t, err)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())

	for _, expected := range expectedTables {
		require.Contains(t, tables, expected, "missing table: %s", expected)
	}

	// Verify exact count (10 tables)
	require.Len(t, tables, len(expectedTables), "expected exactly %d tables, got %d: %v", len(expectedTables), len(tables), tables)
}

func TestOpen_IdempotentMigration(t *testing.T) {
	// Use a temp file so we can close and reopen the same database.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	ctx := context.Background()
	logger := zerolog.Nop()

	// First open: creates the database and schema.
	store1, err := Open(ctx, dbPath, 0, logger)
	require.NoError(t, err)

	// Insert a row so we can verify data persists.
	_, err = store1.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		100, "2026-01-01T00:00:00Z", 5, "live")
	require.NoError(t, err)

	require.NoError(t, store1.Close())

	// Second open: re-applies schema (should not error or lose data).
	store2, err := Open(ctx, dbPath, 0, logger)
	require.NoError(t, err)

	// Verify the inserted row still exists.
	var height int64
	err = store2.db.QueryRowContext(ctx, "SELECT height FROM processed_blocks WHERE height = 100").Scan(&height)
	require.NoError(t, err)
	require.Equal(t, int64(100), height)

	require.NoError(t, store2.Close())
}

func TestOpen_WALMode(t *testing.T) {
	// WAL mode cannot be verified on in-memory databases (they report "memory").
	// Use a temp file database to verify WAL is active.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal_test.db")

	store, err := Open(context.Background(), dbPath, 0, zerolog.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	var journalMode string
	err = store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	require.Equal(t, "wal", journalMode)
}

func TestOpen_ForeignKeysEnabled(t *testing.T) {
	store := newTestStore(t)

	var fkEnabled int
	err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	require.NoError(t, err)
	require.Equal(t, 1, fkEnabled)
}

func TestOpen_SynchronousNormal(t *testing.T) {
	store := newTestStore(t)

	var synchronous int
	err := store.db.QueryRow("PRAGMA synchronous").Scan(&synchronous)
	require.NoError(t, err)
	// synchronous=NORMAL is value 1
	require.Equal(t, 1, synchronous)
}

func TestOpen_SingleWriter(t *testing.T) {
	store := newTestStore(t)

	stats := store.db.Stats()
	require.Equal(t, 1, stats.MaxOpenConnections, "expected single-writer connection pool")
}

func TestOpen_SchemaVersionTracked(t *testing.T) {
	store := newTestStore(t)

	var version int
	err := store.db.QueryRow("SELECT version FROM schema_version").Scan(&version)
	require.NoError(t, err)
	require.Equal(t, 1, version)
}

func TestOpen_InvalidPath(t *testing.T) {
	// Attempt to open a database at a non-existent directory.
	_, err := Open(context.Background(), "/nonexistent/dir/test.db", 0, zerolog.Nop())
	require.Error(t, err)
}

func TestOpen_AllIndexesCreated(t *testing.T) {
	store := newTestStore(t)

	expectedIndexes := []string{
		"idx_settlements_supplier",
		"idx_settlements_service",
		"idx_settlements_block_ts",
		"idx_settlements_height",
		"idx_settlements_session",
		"idx_reward_dist_address",
		"idx_overservice_block_ts",
		"idx_reimbursement_block_ts",
		"idx_reimbursement_link",
	}

	rows, err := store.db.Query("SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%' ORDER BY name")
	require.NoError(t, err)
	defer rows.Close()

	var indexes []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexes = append(indexes, name)
	}
	require.NoError(t, rows.Err())

	for _, expected := range expectedIndexes {
		require.Contains(t, indexes, expected, "missing index: %s", expected)
	}
}

func TestOpen_ForeignKeyCascadeDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert a settlement.
	result, err := store.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, "2026-01-01T00:00:00Z", "settled",
		"pokt1supplier", "pokt1app", "svc1", 99)
	require.NoError(t, err)

	settlementID, err := result.LastInsertId()
	require.NoError(t, err)

	// Insert a reward distribution referencing the settlement.
	_, err = store.db.ExecContext(ctx,
		"INSERT INTO reward_distributions (settlement_id, address, amount_upokt) VALUES (?, ?, ?)",
		settlementID, "pokt1addr", 1000)
	require.NoError(t, err)

	// Verify the reward distribution exists.
	var count int
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions WHERE settlement_id = ?", settlementID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Delete the settlement; CASCADE should delete the reward distribution.
	_, err = store.db.ExecContext(ctx, "DELETE FROM settlements WHERE id = ?", settlementID)
	require.NoError(t, err)

	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions WHERE settlement_id = ?", settlementID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "reward distribution should be cascade-deleted")
}

func TestOpen_UniqueConstraints(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert a settlement.
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, "2026-01-01T00:00:00Z", "settled",
		"pokt1supplier", "pokt1app", "svc1", 99)
	require.NoError(t, err)

	// INSERT OR IGNORE with duplicate key should not error.
	result, err := store.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, "2026-01-01T00:00:00Z", "settled",
		"pokt1supplier", "pokt1app", "svc1", 99)
	require.NoError(t, err)

	rowsAffected, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(0), rowsAffected, "duplicate insert should be ignored")

	// Verify only one row exists.
	var count int
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestOpen_CheckConstraints(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Invalid event_type should fail.
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, "2026-01-01T00:00:00Z", "invalid_type",
		"pokt1supplier", "pokt1app", "svc1", 99)
	require.Error(t, err, "CHECK constraint should reject invalid event_type")

	// Invalid source should fail.
	_, err = store.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		100, "2026-01-01T00:00:00Z", 5, "invalid_source")
	require.Error(t, err, "CHECK constraint should reject invalid source")
}

func TestClose_CancelsContext(t *testing.T) {
	// Verify that Close cancels the internal context (used by retention goroutine).
	// We test this by verifying Close does not panic and returns no error.
	store, err := Open(context.Background(), ":memory:", 0, zerolog.Nop())
	require.NoError(t, err)

	require.NoError(t, store.Close())
}

func TestOpen_TempFileDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Verify file does not exist before opening.
	_, err := os.Stat(dbPath)
	require.True(t, os.IsNotExist(err))

	store, err := Open(context.Background(), dbPath, 0, zerolog.Nop())
	require.NoError(t, err)

	// Verify database file was created.
	_, err = os.Stat(dbPath)
	require.NoError(t, err)

	require.NoError(t, store.Close())
}

// --- Test helpers for InsertBlockEvents ---

var testBlockTimestamp = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func makeSettlement(height int64, eventType string, supplier string, app string, session int64) Settlement {
	return Settlement{
		BlockHeight:              height,
		BlockTimestamp:           testBlockTimestamp,
		EventType:                eventType,
		SupplierOperatorAddress:  supplier,
		ApplicationAddress:       app,
		ServiceID:                "svc1",
		SessionEndBlockHeight:    session,
		ClaimProofStatus:         1,
		ClaimedUpokt:             100000,
		NumRelays:                50,
		NumClaimedComputeUnits:   100,
		NumEstimatedComputeUnits: 200,
		ProofRequirement:         1,
		EstimatedRelays:          100,
		DifficultyMultiplier:     2.0,
	}
}

func makeOverserviceEvent(height int64, supplier string, app string) OverserviceEvent {
	return OverserviceEvent{
		BlockHeight:             height,
		BlockTimestamp:          testBlockTimestamp,
		ApplicationAddress:      app,
		SupplierOperatorAddress: supplier,
		ExpectedBurnUpokt:       50000,
		EffectiveBurnUpokt:      30000,
	}
}

func makeReimbursementEvent(height int64, supplier string, app string) ReimbursementEvent {
	return ReimbursementEvent{
		BlockHeight:             height,
		BlockTimestamp:          testBlockTimestamp,
		ApplicationAddress:      app,
		SupplierOperatorAddress: supplier,
		SupplierOwnerAddress:    "pokt1owner",
		ServiceID:               "svc1",
		SessionID:               "sess1",
		AmountUpokt:             20000,
	}
}

func makeProcessedBlock(height int64, source string, eventCount int) ProcessedBlock {
	return ProcessedBlock{
		Height:         height,
		BlockTimestamp: testBlockTimestamp,
		EventCount:     eventCount,
		Source:         source,
	}
}

// --- InsertBlockEvents tests ---

func TestInsertBlockEvents_SingleBlock(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 4)
	settlements := []Settlement{
		makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95),
		makeSettlement(100, "expired", "pokt1supp2", "pokt1app2", 96),
	}
	settlements[1].ExpirationReason = "proof_window_elapsed"

	overservices := []OverserviceEvent{
		makeOverserviceEvent(100, "pokt1supp1", "pokt1app1"),
	}
	reimbursements := []ReimbursementEvent{
		makeReimbursementEvent(100, "pokt1supp1", "pokt1app1"),
	}

	rewardDists := map[int][]RewardDistribution{
		0: {
			{Address: "pokt1supp1", AmountUpokt: 80000},
			{Address: "pokt1dao", AmountUpokt: 20000},
		},
	}

	err := s.InsertBlockEvents(ctx, block, settlements, rewardDists, overservices, reimbursements)
	require.NoError(t, err)

	// Verify settlements count.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Verify overservice events count.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify reimbursement events count.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reimbursement_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify reward distributions count.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Verify processed_blocks.
	var height int64
	var eventCount int
	var source string
	err = s.db.QueryRowContext(ctx, "SELECT height, event_count, source FROM processed_blocks WHERE height = 100").
		Scan(&height, &eventCount, &source)
	require.NoError(t, err)
	require.Equal(t, int64(100), height)
	require.Equal(t, 4, eventCount)
	require.Equal(t, "live", source)

	// Verify expired settlement has expiration_reason.
	var reason string
	err = s.db.QueryRowContext(ctx, "SELECT expiration_reason FROM settlements WHERE event_type = 'expired'").
		Scan(&reason)
	require.NoError(t, err)
	require.Equal(t, "proof_window_elapsed", reason)
}

func TestInsertBlockEvents_Deduplication(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 1)
	settlements := []Settlement{
		makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95),
	}

	// First insert.
	err := s.InsertBlockEvents(ctx, block, settlements, nil, nil, nil)
	require.NoError(t, err)

	// Second insert with same data (e.g., backfill catching the same block).
	err = s.InsertBlockEvents(ctx, block, settlements, nil, nil, nil)
	require.NoError(t, err)

	// Verify only 1 settlement row.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify only 1 processed_blocks row.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM processed_blocks").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestInsertBlockEvents_RewardDistributions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 1)
	settlements := []Settlement{
		makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95),
	}
	rewardDists := map[int][]RewardDistribution{
		0: {
			{Address: "pokt1supp1", AmountUpokt: 60000},
			{Address: "pokt1owner1", AmountUpokt: 30000},
			{Address: "pokt1dao", AmountUpokt: 10000},
		},
	}

	err := s.InsertBlockEvents(ctx, block, settlements, rewardDists, nil, nil)
	require.NoError(t, err)

	// Verify 3 reward distribution rows.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 3, count)

	// Verify they reference the correct settlement.
	var settlementID int64
	err = s.db.QueryRowContext(ctx, "SELECT id FROM settlements LIMIT 1").Scan(&settlementID)
	require.NoError(t, err)

	rows, err := s.db.QueryContext(ctx,
		"SELECT address, amount_upokt FROM reward_distributions WHERE settlement_id = ? ORDER BY amount_upokt DESC",
		settlementID)
	require.NoError(t, err)
	defer rows.Close()

	type rd struct {
		address string
		amount  int64
	}
	var results []rd
	for rows.Next() {
		var r rd
		require.NoError(t, rows.Scan(&r.address, &r.amount))
		results = append(results, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, results, 3)

	require.Equal(t, "pokt1supp1", results[0].address)
	require.Equal(t, int64(60000), results[0].amount)
	require.Equal(t, "pokt1owner1", results[1].address)
	require.Equal(t, int64(30000), results[1].amount)
	require.Equal(t, "pokt1dao", results[2].address)
	require.Equal(t, int64(10000), results[2].amount)
}

func TestInsertBlockEvents_RewardDistributions_SkippedOnDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 1)
	settlements := []Settlement{
		makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95),
	}

	// First insert with original rewards.
	rewardDists1 := map[int][]RewardDistribution{
		0: {
			{Address: "pokt1supp1", AmountUpokt: 60000},
			{Address: "pokt1dao", AmountUpokt: 10000},
		},
	}
	err := s.InsertBlockEvents(ctx, block, settlements, rewardDists1, nil, nil)
	require.NoError(t, err)

	// Second insert with different rewards (should be skipped because settlement is duplicate).
	rewardDists2 := map[int][]RewardDistribution{
		0: {
			{Address: "pokt1different", AmountUpokt: 99999},
		},
	}
	err = s.InsertBlockEvents(ctx, block, settlements, rewardDists2, nil, nil)
	require.NoError(t, err)

	// Verify only original rewards exist (2 rows, not 3).
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count, "duplicate settlement should not create new reward distributions")

	// Verify "pokt1different" was never inserted.
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM reward_distributions WHERE address = 'pokt1different'").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestInsertBlockEvents_EmptyBlock(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 0)

	err := s.InsertBlockEvents(ctx, block, nil, nil, nil, nil)
	require.NoError(t, err)

	// Verify processed_blocks has the row.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM processed_blocks WHERE height = 100").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify other tables are empty.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reimbursement_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestInsertBlockEvents_AllEventTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 4)
	settlements := []Settlement{
		makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95),
		makeSettlement(100, "expired", "pokt1supp2", "pokt1app2", 96),
		makeSettlement(100, "slashed", "pokt1supp3", "pokt1app3", 97),
		makeSettlement(100, "discarded", "pokt1supp4", "pokt1app4", 98),
	}
	settlements[2].SlashPenaltyUpokt = 50000
	settlements[3].ErrorMessage = "invalid proof"

	err := s.InsertBlockEvents(ctx, block, settlements, nil, nil, nil)
	require.NoError(t, err)

	// Verify 4 rows with correct event types.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 4, count)

	for _, eventType := range []string{"settled", "expired", "slashed", "discarded"} {
		err = s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM settlements WHERE event_type = ?", eventType).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, "expected 1 row for event_type %s", eventType)
	}

	// Verify slashed penalty stored.
	var penalty int64
	err = s.db.QueryRowContext(ctx,
		"SELECT slash_penalty_upokt FROM settlements WHERE event_type = 'slashed'").Scan(&penalty)
	require.NoError(t, err)
	require.Equal(t, int64(50000), penalty)

	// Verify discarded error message stored.
	var errMsg string
	err = s.db.QueryRowContext(ctx,
		"SELECT error_message FROM settlements WHERE event_type = 'discarded'").Scan(&errMsg)
	require.NoError(t, err)
	require.Equal(t, "invalid proof", errMsg)
}

func TestLastProcessedHeight_EmptyDB(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	height, err := s.LastProcessedHeight(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), height)
}

func TestLastProcessedHeight_AfterInserts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert blocks out of order.
	for _, h := range []int64{100, 200, 150} {
		block := makeProcessedBlock(h, "live", 0)
		err := s.InsertBlockEvents(ctx, block, nil, nil, nil, nil)
		require.NoError(t, err)
	}

	height, err := s.LastProcessedHeight(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(200), height)
}

func TestLastProcessedHeight_GapDetection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert blocks with a gap at 102.
	for _, h := range []int64{100, 101, 103} {
		block := makeProcessedBlock(h, "live", 0)
		err := s.InsertBlockEvents(ctx, block, nil, nil, nil, nil)
		require.NoError(t, err)
	}

	// LastProcessedHeight returns MAX, not contiguous max.
	height, err := s.LastProcessedHeight(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(103), height)
}

func TestInsertBlockEvents_OverserviceFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	block := makeProcessedBlock(100, "live", 1)
	settlement := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	settlement.IsOverserviced = true
	settlement.EffectiveBurnUpokt = 30000
	settlement.OverserviceDiffUpokt = 20000

	err := s.InsertBlockEvents(ctx, block, []Settlement{settlement}, nil, nil, nil)
	require.NoError(t, err)

	// Verify overservice fields are stored correctly.
	var isOverserviced int
	var effectiveBurn, overserviceDiff int64
	err = s.db.QueryRowContext(ctx,
		"SELECT is_overserviced, effective_burn_upokt, overservice_diff_upokt FROM settlements WHERE block_height = 100").
		Scan(&isOverserviced, &effectiveBurn, &overserviceDiff)
	require.NoError(t, err)
	require.Equal(t, 1, isOverserviced, "is_overserviced should be 1 (true)")
	require.Equal(t, int64(30000), effectiveBurn)
	require.Equal(t, int64(20000), overserviceDiff)
}

// --- Retention cleanup tests ---

// newTestStoreWithRetention creates an in-memory SQLiteStore with the specified retention duration.
func newTestStoreWithRetention(t *testing.T, retention time.Duration) *SQLiteStore {
	t.Helper()

	store, err := Open(context.Background(), ":memory:", retention, zerolog.Nop())
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}

func TestRunRetentionCleanup_DeletesOldData(t *testing.T) {
	retention := 30 * 24 * time.Hour // 30 days
	s := newTestStoreWithRetention(t, retention)
	ctx := context.Background()

	now := time.Now()
	recentTS := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339) // 10 days ago (within retention)
	oldTS := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339)    // 40 days ago (outside retention)

	// Insert 2 recent settlements with reward distributions.
	for i, supplier := range []string{"pokt1supp1", "pokt1supp2"} {
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO settlements (block_height, block_timestamp, event_type,
			 supplier_operator_address, application_address, service_id,
			 session_end_block_height)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			int64(100+i), recentTS, "settled", supplier, "pokt1app", "svc1", int64(95+i))
		require.NoError(t, err)

		sid, _ := result.LastInsertId()
		_, err = s.db.ExecContext(ctx,
			"INSERT INTO reward_distributions (settlement_id, address, amount_upokt) VALUES (?, ?, ?)",
			sid, supplier, 50000)
		require.NoError(t, err)
	}

	// Insert 2 old settlements with reward distributions.
	for i, supplier := range []string{"pokt1supp3", "pokt1supp4"} {
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO settlements (block_height, block_timestamp, event_type,
			 supplier_operator_address, application_address, service_id,
			 session_end_block_height)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			int64(200+i), oldTS, "settled", supplier, "pokt1app", "svc1", int64(195+i))
		require.NoError(t, err)

		sid, _ := result.LastInsertId()
		_, err = s.db.ExecContext(ctx,
			"INSERT INTO reward_distributions (settlement_id, address, amount_upokt) VALUES (?, ?, ?)",
			sid, supplier, 50000)
		require.NoError(t, err)
	}

	// Insert old overservice and reimbursement events.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO overservice_events (block_height, block_timestamp, application_address,
		 supplier_operator_address, expected_burn_upokt, effective_burn_upokt)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		200, oldTS, "pokt1app", "pokt1supp3", 50000, 30000)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO reimbursement_events (block_height, block_timestamp, application_address,
		 supplier_operator_address, supplier_owner_address, service_id, session_id, amount_upokt)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		200, oldTS, "pokt1app", "pokt1supp3", "pokt1owner", "svc1", "sess1", 20000)
	require.NoError(t, err)

	// Insert processed_blocks.
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		100, recentTS, 2, "live")
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		200, oldTS, 2, "live")
	require.NoError(t, err)

	// Run cleanup.
	result, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	// Verify counts.
	require.Equal(t, int64(2), result.SettlementsDeleted)
	require.Equal(t, int64(2), result.RewardDistributionsDeleted)
	require.Equal(t, int64(1), result.OverserviceEventsDeleted)
	require.Equal(t, int64(1), result.ReimbursementEventsDeleted)
	require.Equal(t, int64(1), result.ProcessedBlocksDeleted)

	// Verify 2 recent settlements remain.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Verify 2 recent reward distributions remain.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Verify overservice events cleaned.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// Verify reimbursement events cleaned.
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reimbursement_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// Verify processed_blocks: 1 remains (recent).
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM processed_blocks").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRunRetentionCleanup_HourlyMultiplier(t *testing.T) {
	retention := 30 * 24 * time.Hour // 30 days, hourly cutoff = 90 days
	s := newTestStoreWithRetention(t, retention)
	ctx := context.Background()

	now := time.Now()
	withinHourlyTS := now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)   // 60 days ago (within 3x=90)
	outsideHourlyTS := now.Add(-100 * 24 * time.Hour).Format(time.RFC3339) // 100 days ago (outside 3x=90)

	// Insert hourly service summaries.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO hourly_summaries_service (hour_start, service_id, claims_settled) VALUES (?, ?, ?)`,
		withinHourlyTS, "svc1", 10)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO hourly_summaries_service (hour_start, service_id, claims_settled) VALUES (?, ?, ?)`,
		outsideHourlyTS, "svc1", 5)
	require.NoError(t, err)

	// Insert hourly network summaries.
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO hourly_summaries_network (hour_start, claims_settled) VALUES (?, ?)`,
		withinHourlyTS, 10)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO hourly_summaries_network (hour_start, claims_settled) VALUES (?, ?)`,
		outsideHourlyTS, 5)
	require.NoError(t, err)

	result, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	require.Equal(t, int64(1), result.HourlySummaryServiceDeleted)
	require.Equal(t, int64(1), result.HourlySummaryNetworkDeleted)

	// Verify 1 remains in each table.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM hourly_summaries_service").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM hourly_summaries_network").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRunRetentionCleanup_DailyMultiplier(t *testing.T) {
	retention := 30 * 24 * time.Hour // 30 days, daily cutoff = 180 days
	s := newTestStoreWithRetention(t, retention)
	ctx := context.Background()

	now := time.Now()
	withinDailyTS := now.Add(-150 * 24 * time.Hour).Format(time.RFC3339)  // 150 days ago (within 6x=180)
	outsideDailyTS := now.Add(-200 * 24 * time.Hour).Format(time.RFC3339) // 200 days ago (outside 6x=180)

	// Insert daily service summaries.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO daily_summaries_service (day_date, service_id, claims_settled) VALUES (?, ?, ?)`,
		withinDailyTS, "svc1", 10)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO daily_summaries_service (day_date, service_id, claims_settled) VALUES (?, ?, ?)`,
		outsideDailyTS, "svc1", 5)
	require.NoError(t, err)

	// Insert daily network summaries.
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO daily_summaries_network (day_date, claims_settled) VALUES (?, ?)`,
		withinDailyTS, 10)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO daily_summaries_network (day_date, claims_settled) VALUES (?, ?)`,
		outsideDailyTS, 5)
	require.NoError(t, err)

	result, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	require.Equal(t, int64(1), result.DailySummaryServiceDeleted)
	require.Equal(t, int64(1), result.DailySummaryNetworkDeleted)

	// Verify 1 remains in each table.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM daily_summaries_service").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM daily_summaries_network").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRunRetentionCleanup_ZeroRetention(t *testing.T) {
	s := newTestStore(t) // retention=0
	ctx := context.Background()

	now := time.Now()
	oldTS := now.Add(-365 * 24 * time.Hour).Format(time.RFC3339) // 1 year ago

	// Insert data at various ages.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, oldTS, "settled", "pokt1supp1", "pokt1app1", "svc1", 95)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO overservice_events (block_height, block_timestamp, application_address,
		 supplier_operator_address, expected_burn_upokt, effective_burn_upokt)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		100, oldTS, "pokt1app1", "pokt1supp1", 50000, 30000)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		100, oldTS, 1, "live")
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO hourly_summaries_service (hour_start, service_id, claims_settled) VALUES (?, ?, ?)`,
		oldTS, "svc1", 10)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO daily_summaries_network (day_date, claims_settled) VALUES (?, ?)`,
		oldTS, 5)
	require.NoError(t, err)

	// Run cleanup with retention=0.
	result, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	// All counts should be zero (nothing deleted).
	require.Equal(t, int64(0), result.SettlementsDeleted)
	require.Equal(t, int64(0), result.RewardDistributionsDeleted)
	require.Equal(t, int64(0), result.OverserviceEventsDeleted)
	require.Equal(t, int64(0), result.ReimbursementEventsDeleted)
	require.Equal(t, int64(0), result.ProcessedBlocksDeleted)
	require.Equal(t, int64(0), result.HourlySummaryServiceDeleted)
	require.Equal(t, int64(0), result.HourlySummaryNetworkDeleted)
	require.Equal(t, int64(0), result.DailySummaryServiceDeleted)
	require.Equal(t, int64(0), result.DailySummaryNetworkDeleted)

	// Verify ALL data is still present.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM processed_blocks").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM hourly_summaries_service").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM daily_summaries_network").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRunRetentionCleanup_RewardDistributionExplicitDelete(t *testing.T) {
	retention := 30 * 24 * time.Hour
	s := newTestStoreWithRetention(t, retention)
	ctx := context.Background()

	now := time.Now()
	oldTS := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339) // 40 days ago

	// Insert settlement with reward distributions.
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, oldTS, "settled", "pokt1supp1", "pokt1app1", "svc1", 95)
	require.NoError(t, err)

	sid, _ := result.LastInsertId()
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO reward_distributions (settlement_id, address, amount_upokt) VALUES (?, ?, ?)",
		sid, "pokt1supp1", 60000)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO reward_distributions (settlement_id, address, amount_upokt) VALUES (?, ?, ?)",
		sid, "pokt1dao", 10000)
	require.NoError(t, err)

	// Run cleanup.
	cleanupResult, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	require.Equal(t, int64(1), cleanupResult.SettlementsDeleted)
	require.Equal(t, int64(2), cleanupResult.RewardDistributionsDeleted)

	// Verify both settlement and its reward distributions are gone.
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestRunRetentionCleanup_ReturnsResult(t *testing.T) {
	retention := 30 * 24 * time.Hour
	s := newTestStoreWithRetention(t, retention)
	ctx := context.Background()

	now := time.Now()
	oldTS := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339)

	// Insert one of each type (all old).
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settlements (block_height, block_timestamp, event_type,
		 supplier_operator_address, application_address, service_id,
		 session_end_block_height)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		100, oldTS, "settled", "pokt1supp1", "pokt1app1", "svc1", 95)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO overservice_events (block_height, block_timestamp, application_address,
		 supplier_operator_address, expected_burn_upokt, effective_burn_upokt)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		100, oldTS, "pokt1app1", "pokt1supp1", 50000, 30000)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO reimbursement_events (block_height, block_timestamp, application_address,
		 supplier_operator_address, supplier_owner_address, service_id, session_id, amount_upokt)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		100, oldTS, "pokt1app1", "pokt1supp1", "pokt1owner", "svc1", "sess1", 20000)
	require.NoError(t, err)

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO processed_blocks (height, block_timestamp, event_count, source) VALUES (?, ?, ?, ?)",
		100, oldTS, 1, "live")
	require.NoError(t, err)

	result, err := s.RunRetentionCleanup(ctx)
	require.NoError(t, err)

	// Verify counts.
	require.Equal(t, int64(1), result.SettlementsDeleted)
	require.Equal(t, int64(1), result.OverserviceEventsDeleted)
	require.Equal(t, int64(1), result.ReimbursementEventsDeleted)
	require.Equal(t, int64(1), result.ProcessedBlocksDeleted)

	// Verify cutoff times are reasonable.
	expectedRawCutoff := now.Add(-retention)
	require.WithinDuration(t, expectedRawCutoff, result.RawCutoff, 5*time.Second)
	require.WithinDuration(t, now.Add(-3*retention), result.HourlyCutoff, 5*time.Second)
	require.WithinDuration(t, now.Add(-6*retention), result.DailyCutoff, 5*time.Second)
}

func TestStartRetentionCleanup_StopsOnCancel(t *testing.T) {
	// Open store with retention set.
	s, err := Open(context.Background(), ":memory:", 30*24*time.Hour, zerolog.Nop())
	require.NoError(t, err)

	// Close the store (which cancels the context), verifying the goroutine exits cleanly.
	require.NoError(t, s.Close())

	// If we get here without panicking or hanging, the goroutine lifecycle is correct.
}

// --- Tests for Get/Query/Count methods ---

func TestGetHourlySummaryService(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hourStart := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Not-found case: returns zero-value struct with no error.
	result, err := s.GetHourlySummaryService(ctx, hourStart, "svc1")
	require.NoError(t, err)
	require.Equal(t, HourlySummaryService{}, result)

	// Upsert a summary, then get it back.
	summary := HourlySummaryService{
		HourStart:               hourStart,
		ServiceID:               "svc1",
		ClaimsSettled:           10,
		ClaimsExpired:           2,
		ClaimsSlashed:           1,
		ClaimsDiscarded:         3,
		ClaimedTotalUpokt:       500000,
		EffectiveTotalUpokt:     400000,
		NumRelays:               1000,
		EstimatedRelays:         2000,
		NumComputeUnits:         500,
		EstimatedComputeUnits:   1000,
		OverserviceCount:        5,
		ReimbursementTotalUpokt: 50000,
		ActiveSupplierCount:     3,
	}
	require.NoError(t, s.UpsertHourlySummaryService(ctx, summary))

	result, err = s.GetHourlySummaryService(ctx, hourStart, "svc1")
	require.NoError(t, err)
	require.NotZero(t, result.ID)
	require.Equal(t, hourStart, result.HourStart)
	require.Equal(t, "svc1", result.ServiceID)
	require.Equal(t, int64(10), result.ClaimsSettled)
	require.Equal(t, int64(2), result.ClaimsExpired)
	require.Equal(t, int64(1), result.ClaimsSlashed)
	require.Equal(t, int64(3), result.ClaimsDiscarded)
	require.Equal(t, int64(500000), result.ClaimedTotalUpokt)
	require.Equal(t, int64(400000), result.EffectiveTotalUpokt)
	require.Equal(t, int64(1000), result.NumRelays)
	require.Equal(t, int64(2000), result.EstimatedRelays)
	require.Equal(t, int64(500), result.NumComputeUnits)
	require.Equal(t, int64(1000), result.EstimatedComputeUnits)
	require.Equal(t, int64(5), result.OverserviceCount)
	require.Equal(t, int64(50000), result.ReimbursementTotalUpokt)
	require.Equal(t, int64(3), result.ActiveSupplierCount)

	// Different service ID returns zero-value.
	result, err = s.GetHourlySummaryService(ctx, hourStart, "svc_other")
	require.NoError(t, err)
	require.Equal(t, HourlySummaryService{}, result)
}

func TestGetHourlySummaryNetwork(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hourStart := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Not-found case.
	result, err := s.GetHourlySummaryNetwork(ctx, hourStart)
	require.NoError(t, err)
	require.Equal(t, HourlySummaryNetwork{}, result)

	// Upsert then get.
	summary := HourlySummaryNetwork{
		HourStart:               hourStart,
		ClaimsSettled:           20,
		ClaimsExpired:           4,
		ClaimsSlashed:           2,
		ClaimsDiscarded:         1,
		ClaimedTotalUpokt:       1000000,
		EffectiveTotalUpokt:     800000,
		NumRelays:               5000,
		EstimatedRelays:         10000,
		NumComputeUnits:         2500,
		EstimatedComputeUnits:   5000,
		OverserviceCount:        10,
		ReimbursementTotalUpokt: 100000,
		ActiveSupplierCount:     7,
	}
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, summary))

	result, err = s.GetHourlySummaryNetwork(ctx, hourStart)
	require.NoError(t, err)
	require.NotZero(t, result.ID)
	require.Equal(t, hourStart, result.HourStart)
	require.Equal(t, int64(20), result.ClaimsSettled)
	require.Equal(t, int64(4), result.ClaimsExpired)
	require.Equal(t, int64(2), result.ClaimsSlashed)
	require.Equal(t, int64(1), result.ClaimsDiscarded)
	require.Equal(t, int64(1000000), result.ClaimedTotalUpokt)
	require.Equal(t, int64(800000), result.EffectiveTotalUpokt)
	require.Equal(t, int64(5000), result.NumRelays)
	require.Equal(t, int64(10000), result.EstimatedRelays)
	require.Equal(t, int64(2500), result.NumComputeUnits)
	require.Equal(t, int64(5000), result.EstimatedComputeUnits)
	require.Equal(t, int64(10), result.OverserviceCount)
	require.Equal(t, int64(100000), result.ReimbursementTotalUpokt)
	require.Equal(t, int64(7), result.ActiveSupplierCount)

	// Different hour returns zero-value.
	otherHour := hourStart.Add(time.Hour)
	result, err = s.GetHourlySummaryNetwork(ctx, otherHour)
	require.NoError(t, err)
	require.Equal(t, HourlySummaryNetwork{}, result)
}

func TestGetDailySummaryService(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dayDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// Not-found case.
	result, err := s.GetDailySummaryService(ctx, dayDate, "svc1")
	require.NoError(t, err)
	require.Equal(t, DailySummaryService{}, result)

	// Upsert then get.
	summary := DailySummaryService{
		DayDate:                 dayDate,
		ServiceID:               "svc1",
		ClaimsSettled:           50,
		ClaimsExpired:           10,
		ClaimsSlashed:           5,
		ClaimsDiscarded:         2,
		ClaimedTotalUpokt:       5000000,
		EffectiveTotalUpokt:     4000000,
		NumRelays:               25000,
		EstimatedRelays:         50000,
		NumComputeUnits:         12500,
		EstimatedComputeUnits:   25000,
		OverserviceCount:        20,
		ReimbursementTotalUpokt: 500000,
		ActiveSupplierCount:     15,
	}
	require.NoError(t, s.UpsertDailySummaryService(ctx, summary))

	result, err = s.GetDailySummaryService(ctx, dayDate, "svc1")
	require.NoError(t, err)
	require.NotZero(t, result.ID)
	require.Equal(t, dayDate, result.DayDate)
	require.Equal(t, "svc1", result.ServiceID)
	require.Equal(t, int64(50), result.ClaimsSettled)
	require.Equal(t, int64(10), result.ClaimsExpired)
	require.Equal(t, int64(5), result.ClaimsSlashed)
	require.Equal(t, int64(2), result.ClaimsDiscarded)
	require.Equal(t, int64(5000000), result.ClaimedTotalUpokt)
	require.Equal(t, int64(4000000), result.EffectiveTotalUpokt)
	require.Equal(t, int64(25000), result.NumRelays)
	require.Equal(t, int64(50000), result.EstimatedRelays)
	require.Equal(t, int64(12500), result.NumComputeUnits)
	require.Equal(t, int64(25000), result.EstimatedComputeUnits)
	require.Equal(t, int64(20), result.OverserviceCount)
	require.Equal(t, int64(500000), result.ReimbursementTotalUpokt)
	require.Equal(t, int64(15), result.ActiveSupplierCount)

	// Different service returns zero-value.
	result, err = s.GetDailySummaryService(ctx, dayDate, "svc_other")
	require.NoError(t, err)
	require.Equal(t, DailySummaryService{}, result)
}

func TestGetDailySummaryNetwork(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dayDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// Not-found case.
	result, err := s.GetDailySummaryNetwork(ctx, dayDate)
	require.NoError(t, err)
	require.Equal(t, DailySummaryNetwork{}, result)

	// Upsert then get.
	summary := DailySummaryNetwork{
		DayDate:                 dayDate,
		ClaimsSettled:           100,
		ClaimsExpired:           20,
		ClaimsSlashed:           10,
		ClaimsDiscarded:         5,
		ClaimedTotalUpokt:       10000000,
		EffectiveTotalUpokt:     8000000,
		NumRelays:               50000,
		EstimatedRelays:         100000,
		NumComputeUnits:         25000,
		EstimatedComputeUnits:   50000,
		OverserviceCount:        40,
		ReimbursementTotalUpokt: 1000000,
		ActiveSupplierCount:     30,
	}
	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, summary))

	result, err = s.GetDailySummaryNetwork(ctx, dayDate)
	require.NoError(t, err)
	require.NotZero(t, result.ID)
	require.Equal(t, dayDate, result.DayDate)
	require.Equal(t, int64(100), result.ClaimsSettled)
	require.Equal(t, int64(20), result.ClaimsExpired)
	require.Equal(t, int64(10), result.ClaimsSlashed)
	require.Equal(t, int64(5), result.ClaimsDiscarded)
	require.Equal(t, int64(10000000), result.ClaimedTotalUpokt)
	require.Equal(t, int64(8000000), result.EffectiveTotalUpokt)
	require.Equal(t, int64(50000), result.NumRelays)
	require.Equal(t, int64(100000), result.EstimatedRelays)
	require.Equal(t, int64(25000), result.NumComputeUnits)
	require.Equal(t, int64(50000), result.EstimatedComputeUnits)
	require.Equal(t, int64(40), result.OverserviceCount)
	require.Equal(t, int64(1000000), result.ReimbursementTotalUpokt)
	require.Equal(t, int64(30), result.ActiveSupplierCount)

	// Different date returns zero-value.
	otherDate := dayDate.AddDate(0, 0, 1)
	result, err = s.GetDailySummaryNetwork(ctx, otherDate)
	require.NoError(t, err)
	require.Equal(t, DailySummaryNetwork{}, result)
}

func TestDistinctServiceIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty table returns empty slice (not nil).
	ids, err := s.DistinctServiceIDs(ctx)
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Len(t, ids, 0)

	// Insert settlements with different service IDs.
	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.ServiceID = "svc_b"
	s2 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app2", 96)
	s2.ServiceID = "svc_a"
	s3 := makeSettlement(101, "settled", "pokt1supp1", "pokt1app1", 97)
	s3.ServiceID = "svc_b" // duplicate

	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2}, nil, nil, nil))

	block2 := makeProcessedBlock(101, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s3}, nil, nil, nil))

	ids, err = s.DistinctServiceIDs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"svc_a", "svc_b"}, ids)
}

func TestCountActiveSuppliers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	from := baseTime
	to := baseTime.Add(2 * time.Hour)

	// No data returns 0.
	count, err := s.CountActiveSuppliers(ctx, from, to, "")
	require.NoError(t, err)
	require.Equal(t, int64(0), count)

	// Insert settlements from multiple suppliers across time range.
	// Block at baseTime (within range).
	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.ServiceID = "svc1"
	s1.BlockTimestamp = baseTime
	s2 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app2", 96)
	s2.ServiceID = "svc1"
	s2.BlockTimestamp = baseTime
	s3 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app3", 97) // dup supplier
	s3.ServiceID = "svc2"
	s3.BlockTimestamp = baseTime

	block1 := ProcessedBlock{Height: 100, BlockTimestamp: baseTime, EventCount: 3, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2, s3}, nil, nil, nil))

	// Block at baseTime+1h (within range).
	s4 := makeSettlement(101, "settled", "pokt1supp3", "pokt1app1", 98)
	s4.ServiceID = "svc1"
	s4.BlockTimestamp = baseTime.Add(time.Hour)

	block2 := ProcessedBlock{Height: 101, BlockTimestamp: baseTime.Add(time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s4}, nil, nil, nil))

	// Block at baseTime+3h (outside range).
	s5 := makeSettlement(102, "settled", "pokt1supp4", "pokt1app1", 99)
	s5.ServiceID = "svc1"
	s5.BlockTimestamp = baseTime.Add(3 * time.Hour)

	block3 := ProcessedBlock{Height: 102, BlockTimestamp: baseTime.Add(3 * time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block3, []Settlement{s5}, nil, nil, nil))

	// Count all suppliers in range (pokt1supp1, pokt1supp2, pokt1supp3 = 3 distinct).
	count, err = s.CountActiveSuppliers(ctx, from, to, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// Count only svc1 suppliers in range (pokt1supp1, pokt1supp2, pokt1supp3 = 3).
	count, err = s.CountActiveSuppliers(ctx, from, to, "svc1")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// Count svc2 suppliers in range (pokt1supp1 only = 1).
	count, err = s.CountActiveSuppliers(ctx, from, to, "svc2")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	// Count for empty range returns 0.
	count, err = s.CountActiveSuppliers(ctx, baseTime.Add(5*time.Hour), baseTime.Add(6*time.Hour), "")
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
}

func TestQuerySettlementsForPeriod(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Insert settlements at different timestamps.
	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.BlockTimestamp = baseTime
	s1.IsOverserviced = true
	s1.EffectiveBurnUpokt = 30000
	s1.OverserviceDiffUpokt = 20000

	block1 := ProcessedBlock{Height: 100, BlockTimestamp: baseTime, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1}, nil, nil, nil))

	s2 := makeSettlement(101, "expired", "pokt1supp2", "pokt1app2", 96)
	s2.BlockTimestamp = baseTime.Add(time.Hour)
	s2.ExpirationReason = "proof_window_elapsed"

	block2 := ProcessedBlock{Height: 101, BlockTimestamp: baseTime.Add(time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s2}, nil, nil, nil))

	s3 := makeSettlement(102, "settled", "pokt1supp3", "pokt1app3", 97)
	s3.BlockTimestamp = baseTime.Add(3 * time.Hour)

	block3 := ProcessedBlock{Height: 102, BlockTimestamp: baseTime.Add(3 * time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block3, []Settlement{s3}, nil, nil, nil))

	// Query range that includes first two but not third.
	from := baseTime
	to := baseTime.Add(2 * time.Hour)
	results, err := s.QuerySettlementsForPeriod(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify first settlement fields.
	require.Equal(t, int64(100), results[0].BlockHeight)
	require.Equal(t, "settled", results[0].EventType)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
	require.Equal(t, "pokt1app1", results[0].ApplicationAddress)
	require.Equal(t, "svc1", results[0].ServiceID)
	require.Equal(t, int64(100000), results[0].ClaimedUpokt)
	require.Equal(t, int64(50), results[0].NumRelays)
	require.True(t, results[0].IsOverserviced, "is_overserviced should roundtrip as true")
	require.Equal(t, int64(30000), results[0].EffectiveBurnUpokt)
	require.Equal(t, int64(20000), results[0].OverserviceDiffUpokt)
	require.Equal(t, baseTime, results[0].BlockTimestamp)

	// Verify second settlement.
	require.Equal(t, int64(101), results[1].BlockHeight)
	require.Equal(t, "expired", results[1].EventType)
	require.Equal(t, "proof_window_elapsed", results[1].ExpirationReason)

	// Empty range returns nil/empty slice.
	results, err = s.QuerySettlementsForPeriod(ctx, baseTime.Add(10*time.Hour), baseTime.Add(11*time.Hour))
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestQueryOverserviceEventsForPeriod(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Insert overservice events at different timestamps.
	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	oe1.BlockTimestamp = baseTime

	block1 := ProcessedBlock{Height: 100, BlockTimestamp: baseTime, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, []OverserviceEvent{oe1}, nil))

	oe2 := makeOverserviceEvent(101, "pokt1supp2", "pokt1app2")
	oe2.BlockTimestamp = baseTime.Add(time.Hour)

	block2 := ProcessedBlock{Height: 101, BlockTimestamp: baseTime.Add(time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, []OverserviceEvent{oe2}, nil))

	oe3 := makeOverserviceEvent(102, "pokt1supp3", "pokt1app3")
	oe3.BlockTimestamp = baseTime.Add(3 * time.Hour)

	block3 := ProcessedBlock{Height: 102, BlockTimestamp: baseTime.Add(3 * time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block3, nil, nil, []OverserviceEvent{oe3}, nil))

	// Query range that includes first two.
	from := baseTime
	to := baseTime.Add(2 * time.Hour)
	results, err := s.QueryOverserviceEventsForPeriod(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Equal(t, int64(100), results[0].BlockHeight)
	require.Equal(t, "pokt1app1", results[0].ApplicationAddress)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
	require.Equal(t, int64(50000), results[0].ExpectedBurnUpokt)
	require.Equal(t, int64(30000), results[0].EffectiveBurnUpokt)
	require.Equal(t, baseTime, results[0].BlockTimestamp)

	require.Equal(t, int64(101), results[1].BlockHeight)

	// Empty range.
	results, err = s.QueryOverserviceEventsForPeriod(ctx, baseTime.Add(10*time.Hour), baseTime.Add(11*time.Hour))
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestQueryReimbursementEventsForPeriod(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	// Insert reimbursement events at different timestamps.
	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.BlockTimestamp = baseTime

	block1 := ProcessedBlock{Height: 100, BlockTimestamp: baseTime, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, nil, []ReimbursementEvent{re1}))

	re2 := makeReimbursementEvent(101, "pokt1supp2", "pokt1app2")
	re2.BlockTimestamp = baseTime.Add(time.Hour)
	re2.SessionID = "sess2" // unique session to avoid UNIQUE constraint

	block2 := ProcessedBlock{Height: 101, BlockTimestamp: baseTime.Add(time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, nil, []ReimbursementEvent{re2}))

	re3 := makeReimbursementEvent(102, "pokt1supp3", "pokt1app3")
	re3.BlockTimestamp = baseTime.Add(3 * time.Hour)
	re3.SessionID = "sess3"

	block3 := ProcessedBlock{Height: 102, BlockTimestamp: baseTime.Add(3 * time.Hour), EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block3, nil, nil, nil, []ReimbursementEvent{re3}))

	// Query range that includes first two.
	from := baseTime
	to := baseTime.Add(2 * time.Hour)
	results, err := s.QueryReimbursementEventsForPeriod(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Equal(t, int64(100), results[0].BlockHeight)
	require.Equal(t, "pokt1app1", results[0].ApplicationAddress)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
	require.Equal(t, "pokt1owner", results[0].SupplierOwnerAddress)
	require.Equal(t, "svc1", results[0].ServiceID)
	require.Equal(t, "sess1", results[0].SessionID)
	require.Equal(t, int64(20000), results[0].AmountUpokt)
	require.Equal(t, baseTime, results[0].BlockTimestamp)

	require.Equal(t, int64(101), results[1].BlockHeight)

	// Empty range.
	results, err = s.QueryReimbursementEventsForPeriod(ctx, baseTime.Add(10*time.Hour), baseTime.Add(11*time.Hour))
	require.NoError(t, err)
	require.Empty(t, results)
}
