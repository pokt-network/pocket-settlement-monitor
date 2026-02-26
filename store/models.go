package store

import "time"

// Settlement represents a single claim settlement event (settled, expired, slashed, or discarded).
// All 4 event types are stored in the same table, differentiated by EventType.
type Settlement struct {
	ID                      int64
	BlockHeight             int64
	BlockTimestamp          time.Time
	EventType               string // "settled", "expired", "slashed", "discarded"
	SupplierOperatorAddress string
	ApplicationAddress      string
	ServiceID               string
	SessionEndBlockHeight   int64
	ClaimProofStatus        int32

	// Settled/Expired fields
	ClaimedUpokt             int64
	NumRelays                int64
	NumClaimedComputeUnits   int64
	NumEstimatedComputeUnits int64
	ProofRequirement         int32

	// Computed fields
	EstimatedRelays      int64
	DifficultyMultiplier float64

	// Overservice correlation
	IsOverserviced       bool
	EffectiveBurnUpokt   int64
	OverserviceDiffUpokt int64

	// Type-specific fields
	ExpirationReason  string // expired events only
	ErrorMessage      string // discarded events only
	SlashPenaltyUpokt int64  // slashed events only
}

// RewardDistribution represents a single reward payout within a settled claim.
// Stored in a normalized table with a foreign key to settlements.
type RewardDistribution struct {
	ID           int64
	SettlementID int64
	Address      string
	AmountUpokt  int64
}

// OverserviceEvent represents an EventApplicationOverserviced event.
type OverserviceEvent struct {
	ID                      int64
	BlockHeight             int64
	BlockTimestamp          time.Time
	ApplicationAddress      string
	SupplierOperatorAddress string
	ExpectedBurnUpokt       int64
	EffectiveBurnUpokt      int64
}

// ReimbursementEvent represents an EventApplicationReimbursementRequest event.
type ReimbursementEvent struct {
	ID                      int64
	BlockHeight             int64
	BlockTimestamp          time.Time
	ApplicationAddress      string
	SupplierOperatorAddress string
	SupplierOwnerAddress    string
	ServiceID               string
	SessionID               string
	AmountUpokt             int64
}

// ProcessedBlock tracks which blocks have been processed and by which source.
type ProcessedBlock struct {
	Height         int64
	BlockTimestamp time.Time
	EventCount     int
	Source         string // "live" or "backfill"
}

// HourlySummaryService holds per-service hourly aggregated metrics.
type HourlySummaryService struct {
	ID                      int64
	HourStart               time.Time
	ServiceID               string
	ClaimsSettled           int64
	ClaimsExpired           int64
	ClaimsSlashed           int64
	ClaimsDiscarded         int64
	ClaimedTotalUpokt       int64
	EffectiveTotalUpokt     int64
	NumRelays               int64
	EstimatedRelays         int64
	NumComputeUnits         int64
	EstimatedComputeUnits   int64
	OverserviceCount        int64
	ReimbursementTotalUpokt int64
	ActiveSupplierCount     int64
}

// HourlySummaryNetwork holds network-wide hourly aggregated metrics.
type HourlySummaryNetwork struct {
	ID                      int64
	HourStart               time.Time
	ClaimsSettled           int64
	ClaimsExpired           int64
	ClaimsSlashed           int64
	ClaimsDiscarded         int64
	ClaimedTotalUpokt       int64
	EffectiveTotalUpokt     int64
	NumRelays               int64
	EstimatedRelays         int64
	NumComputeUnits         int64
	EstimatedComputeUnits   int64
	OverserviceCount        int64
	ReimbursementTotalUpokt int64
	ActiveSupplierCount     int64
}

// DailySummaryService holds per-service daily aggregated metrics.
type DailySummaryService struct {
	ID                      int64
	DayDate                 time.Time
	ServiceID               string
	ClaimsSettled           int64
	ClaimsExpired           int64
	ClaimsSlashed           int64
	ClaimsDiscarded         int64
	ClaimedTotalUpokt       int64
	EffectiveTotalUpokt     int64
	NumRelays               int64
	EstimatedRelays         int64
	NumComputeUnits         int64
	EstimatedComputeUnits   int64
	OverserviceCount        int64
	ReimbursementTotalUpokt int64
	ActiveSupplierCount     int64
}

// DailySummaryNetwork holds network-wide daily aggregated metrics.
type DailySummaryNetwork struct {
	ID                      int64
	DayDate                 time.Time
	ClaimsSettled           int64
	ClaimsExpired           int64
	ClaimsSlashed           int64
	ClaimsDiscarded         int64
	ClaimedTotalUpokt       int64
	EffectiveTotalUpokt     int64
	NumRelays               int64
	EstimatedRelays         int64
	NumComputeUnits         int64
	EstimatedComputeUnits   int64
	OverserviceCount        int64
	ReimbursementTotalUpokt int64
	ActiveSupplierCount     int64
}

// SettlementFilters provides filtering options for settlement queries.
type SettlementFilters struct {
	SupplierOperatorAddress string
	ServiceID               string
	EventType               string
	FromTime                time.Time
	ToTime                  time.Time
	FromHeight              int64
	ToHeight                int64
	Limit                   int
}

// OverserviceFilters provides filtering options for overservice event queries.
type OverserviceFilters struct {
	SupplierOperatorAddress string
	ApplicationAddress      string
	FromTime                time.Time
	ToTime                  time.Time
	FromHeight              int64
	ToHeight                int64
	Limit                   int
}

// ReimbursementFilters provides filtering options for reimbursement event queries.
type ReimbursementFilters struct {
	SupplierOperatorAddress string
	ServiceID               string
	ApplicationAddress      string
	FromTime                time.Time
	ToTime                  time.Time
	FromHeight              int64
	ToHeight                int64
	Limit                   int
}

// SummaryFilters provides filtering options for summary queries.
type SummaryFilters struct {
	ServiceID string
	FromTime  time.Time
	ToTime    time.Time
	Limit     int
}

// RetentionResult reports what was deleted during a retention cleanup cycle.
type RetentionResult struct {
	SettlementsDeleted          int64
	RewardDistributionsDeleted  int64
	OverserviceEventsDeleted    int64
	ReimbursementEventsDeleted  int64
	ProcessedBlocksDeleted      int64
	HourlySummaryServiceDeleted int64
	HourlySummaryNetworkDeleted int64
	DailySummaryServiceDeleted  int64
	DailySummaryNetworkDeleted  int64
	RawCutoff                   time.Time
	HourlyCutoff                time.Time
	DailyCutoff                 time.Time
}
