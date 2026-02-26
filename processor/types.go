package processor

// overserviceKey is the composite key used to correlate overservice events
// with settled claims in the same block. Both event types use different field
// names for the same addresses:
//   - EventClaimSettled: SupplierOperatorAddress, ApplicationAddress
//   - EventApplicationOverserviced: SupplierOperatorAddr, ApplicationAddr
//
// The converter normalizes both to this common key.
type overserviceKey struct {
	SupplierOperatorAddress string
	ApplicationAddress      string
}

// overserviceData holds the burn amounts from an EventApplicationOverserviced event.
// Used during the second pass of correlateOverservice to mark settled claims.
type overserviceData struct {
	ExpectedBurnUpokt  int64
	EffectiveBurnUpokt int64
}

// BlockSummary aggregates per-block statistics for the one-line info log.
// All dimensions are broken down by event type where the data exists.
type BlockSummary struct {
	Height                  int64
	TotalEvents             int
	OverserviceCorrelations int

	// uPOKT by event type
	SettledUpokt     int64
	ExpiredUpokt     int64
	SlashedUpokt     int64
	OverserviceUpokt int64

	// Relays by event type (actual from merkle tree)
	SettledRelays int64
	ExpiredRelays int64

	// Estimated relays by event type (after difficulty expansion)
	SettledEstRelays int64
	ExpiredEstRelays int64

	// Compute units by event type (actual from merkle tree)
	SettledComputeUnits int64
	ExpiredComputeUnits int64

	// Estimated compute units by event type (after difficulty expansion)
	SettledEstComputeUnits int64
	ExpiredEstComputeUnits int64
}
