package store

import (
	"context"
	"time"
)

// Store defines the persistence interface consumed by the processor, reporter,
// and query command phases. Implemented by SQLiteStore.
type Store interface {
	// InsertBlockEvents atomically inserts all events for a single block in one
	// transaction. Uses INSERT OR IGNORE for deduplication via UNIQUE constraints.
	// The rewardDists map keys correspond to the index of the settlement in the
	// settlements slice.
	InsertBlockEvents(ctx context.Context, block ProcessedBlock, settlements []Settlement,
		rewardDists map[int][]RewardDistribution, overservices []OverserviceEvent,
		reimbursements []ReimbursementEvent) error

	// LastProcessedHeight returns the highest block height that has been processed.
	// Returns 0 if no blocks have been processed yet (empty database).
	LastProcessedHeight(ctx context.Context) (int64, error)

	// UpsertHourlySummaryService inserts or updates a per-service hourly summary row.
	UpsertHourlySummaryService(ctx context.Context, summary HourlySummaryService) error

	// UpsertDailySummaryService inserts or updates a per-service daily summary row.
	UpsertDailySummaryService(ctx context.Context, summary DailySummaryService) error

	// UpsertHourlySummaryNetwork inserts or updates a network-wide hourly summary row.
	UpsertHourlySummaryNetwork(ctx context.Context, summary HourlySummaryNetwork) error

	// UpsertDailySummaryNetwork inserts or updates a network-wide daily summary row.
	UpsertDailySummaryNetwork(ctx context.Context, summary DailySummaryNetwork) error

	// GetHourlySummaryService returns the per-service hourly summary for the given
	// hour and service. Returns a zero-value struct if no row exists (not an error).
	GetHourlySummaryService(ctx context.Context, hourStart time.Time, serviceID string) (HourlySummaryService, error)

	// GetHourlySummaryNetwork returns the network-wide hourly summary for the given
	// hour. Returns a zero-value struct if no row exists (not an error).
	GetHourlySummaryNetwork(ctx context.Context, hourStart time.Time) (HourlySummaryNetwork, error)

	// GetDailySummaryService returns the per-service daily summary for the given
	// date and service. Returns a zero-value struct if no row exists (not an error).
	GetDailySummaryService(ctx context.Context, dayDate time.Time, serviceID string) (DailySummaryService, error)

	// GetDailySummaryNetwork returns the network-wide daily summary for the given
	// date. Returns a zero-value struct if no row exists (not an error).
	GetDailySummaryNetwork(ctx context.Context, dayDate time.Time) (DailySummaryNetwork, error)

	// DistinctServiceIDs returns all unique service IDs from the settlements table,
	// sorted alphabetically. Returns an empty slice on an empty table (not an error).
	DistinctServiceIDs(ctx context.Context) ([]string, error)

	// CountActiveSuppliers returns the count of distinct supplier operator addresses
	// in the settlements table within the given time range. When serviceID is non-empty,
	// only suppliers for that service are counted.
	CountActiveSuppliers(ctx context.Context, from, to time.Time, serviceID string) (int64, error)

	// QuerySettlementsForPeriod returns all settlements within the given time range,
	// ordered by block_height and id.
	QuerySettlementsForPeriod(ctx context.Context, from, to time.Time) ([]Settlement, error)

	// QueryOverserviceEventsForPeriod returns all overservice events within the given
	// time range.
	QueryOverserviceEventsForPeriod(ctx context.Context, from, to time.Time) ([]OverserviceEvent, error)

	// QueryReimbursementEventsForPeriod returns all reimbursement events within the
	// given time range.
	QueryReimbursementEventsForPeriod(ctx context.Context, from, to time.Time) ([]ReimbursementEvent, error)

	// QuerySettlementsFiltered queries settlements with optional dynamic filters.
	// Results are ordered by block_height DESC, id DESC (most recent first).
	QuerySettlementsFiltered(ctx context.Context, filters SettlementFilters) ([]Settlement, error)

	// QueryOverserviceFiltered queries overservice events with optional dynamic filters.
	// Results are ordered by block_height DESC, id DESC (most recent first).
	QueryOverserviceFiltered(ctx context.Context, filters OverserviceFilters) ([]OverserviceEvent, error)

	// QueryReimbursementsFiltered queries reimbursement events with optional dynamic filters.
	// Results are ordered by block_height DESC, id DESC (most recent first).
	QueryReimbursementsFiltered(ctx context.Context, filters ReimbursementFilters) ([]ReimbursementEvent, error)

	// QueryHourlySummariesFiltered queries hourly summaries with optional dynamic filters.
	// When ServiceID is set, queries per-service table. When empty, queries network table.
	// Results are ordered by hour_start DESC (most recent first).
	QueryHourlySummariesFiltered(ctx context.Context, filters SummaryFilters) ([]HourlySummaryService, error)

	// QueryDailySummariesFiltered queries daily summaries with optional dynamic filters.
	// When ServiceID is set, queries per-service table. When empty, queries network table.
	// Results are ordered by day_date DESC (most recent first).
	QueryDailySummariesFiltered(ctx context.Context, filters SummaryFilters) ([]DailySummaryService, error)

	// RunRetentionCleanup deletes data older than the configured retention period.
	// Returns a result with per-table deletion counts and cutoff times.
	RunRetentionCleanup(ctx context.Context) (RetentionResult, error)

	// Close releases database resources and stops background goroutines.
	Close() error
}

// Compile-time interface check: SQLiteStore must implement Store.
var _ Store = (*SQLiteStore)(nil)
