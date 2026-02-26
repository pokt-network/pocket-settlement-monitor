package processor

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

// insertTestSettlement inserts a settlement via the store's InsertBlockEvents.
func insertTestSettlement(t *testing.T, ctx context.Context, s *store.SQLiteStore,
	height int64, ts time.Time, eventType, supplier, app, serviceID string,
	claimedUpokt, numRelays, numCU, numEstCU, estRelays int64,
	isOverserviced bool, effectiveBurn int64) {
	t.Helper()

	settlement := store.Settlement{
		BlockHeight:              height,
		BlockTimestamp:           ts,
		EventType:                eventType,
		SupplierOperatorAddress:  supplier,
		ApplicationAddress:       app,
		ServiceID:                serviceID,
		ClaimedUpokt:             claimedUpokt,
		NumRelays:                numRelays,
		NumClaimedComputeUnits:   numCU,
		NumEstimatedComputeUnits: numEstCU,
		EstimatedRelays:          estRelays,
		IsOverserviced:           isOverserviced,
		EffectiveBurnUpokt:       effectiveBurn,
	}

	block := store.ProcessedBlock{
		Height:         height,
		BlockTimestamp: ts,
		EventCount:     1,
		Source:         "live",
	}

	err := s.InsertBlockEvents(ctx, block, []store.Settlement{settlement}, nil, nil, nil)
	require.NoError(t, err)
}

func TestNewReporter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Empty DB: no known service IDs.
	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)
	assert.Empty(t, r.knownServiceIDs)

	// Insert settlements with service IDs.
	insertTestSettlement(t, ctx, s, 100,
		time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		"settled", "pokt1s1", "pokt1a1", "svc1",
		1000, 10, 20, 40, 20, false, 0)
	insertTestSettlement(t, ctx, s, 101,
		time.Date(2026, 1, 15, 10, 31, 0, 0, time.UTC),
		"settled", "pokt1s2", "pokt1a2", "svc2",
		2000, 20, 40, 80, 40, false, 0)

	// New reporter should know about svc1 and svc2.
	r2, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)
	assert.Len(t, r2.knownServiceIDs, 2)
	assert.Contains(t, r2.knownServiceIDs, "svc1")
	assert.Contains(t, r2.knownServiceIDs, "svc2")
}

func TestReportBlockSingleBlock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Pre-insert settlements into DB so CountActiveSuppliers finds them.
	ts := time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC)
	insertTestSettlement(t, ctx, s, 100, ts,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		5000, 200, 100, 200, 400, false, 0)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	settlements := []store.Settlement{{
		EventType:                "settled",
		SupplierOperatorAddress:  "pokt1s1",
		ApplicationAddress:       "pokt1a1",
		ServiceID:                "svc1",
		ClaimedUpokt:             5000,
		NumRelays:                200,
		NumClaimedComputeUnits:   100,
		NumEstimatedComputeUnits: 200,
		EstimatedRelays:          400,
	}}

	err = r.ReportBlock(ctx, 100, ts, settlements, nil, nil)
	require.NoError(t, err)

	// Verify hourly service summary.
	hs := hourStart(ts)
	hourSvc, err := s.GetHourlySummaryService(ctx, hs, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), hourSvc.ClaimsSettled)
	assert.Equal(t, int64(5000), hourSvc.ClaimedTotalUpokt)
	assert.Equal(t, int64(5000), hourSvc.EffectiveTotalUpokt)
	assert.Equal(t, int64(200), hourSvc.NumRelays)
	assert.Equal(t, int64(400), hourSvc.EstimatedRelays)
	assert.Equal(t, int64(100), hourSvc.NumComputeUnits)
	assert.Equal(t, int64(200), hourSvc.EstimatedComputeUnits)
	assert.Equal(t, int64(1), hourSvc.ActiveSupplierCount)

	// Verify hourly network summary.
	hourNet, err := s.GetHourlySummaryNetwork(ctx, hs)
	require.NoError(t, err)
	assert.Equal(t, int64(1), hourNet.ClaimsSettled)
	assert.Equal(t, int64(5000), hourNet.ClaimedTotalUpokt)
	assert.Equal(t, int64(1), hourNet.ActiveSupplierCount)

	// Verify daily summaries.
	ds := dayStart(ts)
	dailySvc, err := s.GetDailySummaryService(ctx, ds, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dailySvc.ClaimsSettled)
	assert.Equal(t, int64(5000), dailySvc.ClaimedTotalUpokt)

	dailyNet, err := s.GetDailySummaryNetwork(ctx, ds)
	require.NoError(t, err)
	assert.Equal(t, int64(1), dailyNet.ClaimsSettled)
}

func TestReportBlockAccumulation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts1 := time.Date(2026, 1, 15, 14, 10, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 15, 14, 20, 0, 0, time.UTC)

	// Insert raw data for CountActiveSuppliers.
	insertTestSettlement(t, ctx, s, 100, ts1,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		3000, 100, 50, 100, 200, false, 0)
	insertTestSettlement(t, ctx, s, 101, ts2,
		"settled", "pokt1s2", "pokt1a2", "svc1",
		4000, 150, 75, 150, 300, false, 0)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	// First block.
	err = r.ReportBlock(ctx, 100, ts1,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s1",
			ApplicationAddress: "pokt1a1", ServiceID: "svc1",
			ClaimedUpokt: 3000, NumRelays: 100, NumClaimedComputeUnits: 50,
			NumEstimatedComputeUnits: 100, EstimatedRelays: 200,
		}}, nil, nil)
	require.NoError(t, err)

	// Second block (same hour).
	err = r.ReportBlock(ctx, 101, ts2,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s2",
			ApplicationAddress: "pokt1a2", ServiceID: "svc1",
			ClaimedUpokt: 4000, NumRelays: 150, NumClaimedComputeUnits: 75,
			NumEstimatedComputeUnits: 150, EstimatedRelays: 300,
		}}, nil, nil)
	require.NoError(t, err)

	// Verify accumulated.
	hs := hourStart(ts1)
	hourSvc, err := s.GetHourlySummaryService(ctx, hs, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), hourSvc.ClaimsSettled)
	assert.Equal(t, int64(7000), hourSvc.ClaimedTotalUpokt)
	assert.Equal(t, int64(250), hourSvc.NumRelays)
	// 2 distinct suppliers in this hour.
	assert.Equal(t, int64(2), hourSvc.ActiveSupplierCount)
}

func TestReportBlockHourBoundary(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts14 := time.Date(2026, 1, 15, 14, 50, 0, 0, time.UTC)
	ts15 := time.Date(2026, 1, 15, 15, 10, 0, 0, time.UTC)

	// Pre-insert for CountActiveSuppliers.
	insertTestSettlement(t, ctx, s, 100, ts14,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		1000, 10, 5, 10, 20, false, 0)
	insertTestSettlement(t, ctx, s, 101, ts15,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		2000, 20, 10, 20, 40, false, 0)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	err = r.ReportBlock(ctx, 100, ts14,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s1",
			ApplicationAddress: "pokt1a1", ServiceID: "svc1",
			ClaimedUpokt: 1000, NumRelays: 10, NumClaimedComputeUnits: 5,
			NumEstimatedComputeUnits: 10, EstimatedRelays: 20,
		}}, nil, nil)
	require.NoError(t, err)

	err = r.ReportBlock(ctx, 101, ts15,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s1",
			ApplicationAddress: "pokt1a1", ServiceID: "svc1",
			ClaimedUpokt: 2000, NumRelays: 20, NumClaimedComputeUnits: 10,
			NumEstimatedComputeUnits: 20, EstimatedRelays: 40,
		}}, nil, nil)
	require.NoError(t, err)

	// Two separate hourly rows.
	h14, err := s.GetHourlySummaryService(ctx, hourStart(ts14), "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h14.ClaimsSettled)
	assert.Equal(t, int64(1000), h14.ClaimedTotalUpokt)

	h15, err := s.GetHourlySummaryService(ctx, hourStart(ts15), "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h15.ClaimsSettled)
	assert.Equal(t, int64(2000), h15.ClaimedTotalUpokt)

	// Daily should accumulate both.
	ds := dayStart(ts14)
	dailySvc, err := s.GetDailySummaryService(ctx, ds, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), dailySvc.ClaimsSettled)
	assert.Equal(t, int64(3000), dailySvc.ClaimedTotalUpokt)
}

func TestReportBlockZeroRows(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts1 := time.Date(2026, 1, 15, 14, 10, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 15, 14, 20, 0, 0, time.UTC)

	// Block 1: introduce svc1.
	insertTestSettlement(t, ctx, s, 100, ts1,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		1000, 10, 5, 10, 20, false, 0)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	err = r.ReportBlock(ctx, 100, ts1,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s1",
			ApplicationAddress: "pokt1a1", ServiceID: "svc1",
			ClaimedUpokt: 1000, NumRelays: 10,
		}}, nil, nil)
	require.NoError(t, err)

	// Block 2: only svc2 has events, but svc1 should get zero rows.
	insertTestSettlement(t, ctx, s, 101, ts2,
		"settled", "pokt1s2", "pokt1a2", "svc2",
		2000, 20, 10, 20, 40, false, 0)

	err = r.ReportBlock(ctx, 101, ts2,
		[]store.Settlement{{
			EventType: "settled", SupplierOperatorAddress: "pokt1s2",
			ApplicationAddress: "pokt1a2", ServiceID: "svc2",
			ClaimedUpokt: 2000, NumRelays: 20,
		}}, nil, nil)
	require.NoError(t, err)

	// svc1 hourly summary should exist (accumulated from block 1 + zero from block 2).
	hs := hourStart(ts1)
	svc1Hour, err := s.GetHourlySummaryService(ctx, hs, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), svc1Hour.ClaimsSettled) // From block 1 only.
	assert.Equal(t, int64(1000), svc1Hour.ClaimedTotalUpokt)

	// svc2 hourly summary should exist.
	svc2Hour, err := s.GetHourlySummaryService(ctx, hs, "svc2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), svc2Hour.ClaimsSettled)
	assert.Equal(t, int64(2000), svc2Hour.ClaimedTotalUpokt)
}

func TestReportBlockReimbursements(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts := time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	reimbursements := []store.ReimbursementEvent{
		{ServiceID: "svc1", AmountUpokt: 500},
		{ServiceID: "svc1", AmountUpokt: 300},
	}

	// Also include a settlement to create the service ID.
	settlements := []store.Settlement{{
		EventType: "settled", SupplierOperatorAddress: "pokt1s1",
		ApplicationAddress: "pokt1a1", ServiceID: "svc1",
		ClaimedUpokt: 1000,
	}}

	err = r.ReportBlock(ctx, 100, ts, settlements, nil, reimbursements)
	require.NoError(t, err)

	hs := hourStart(ts)
	hourSvc, err := s.GetHourlySummaryService(ctx, hs, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(800), hourSvc.ReimbursementTotalUpokt)

	hourNet, err := s.GetHourlySummaryNetwork(ctx, hs)
	require.NoError(t, err)
	assert.Equal(t, int64(800), hourNet.ReimbursementTotalUpokt)
}

func TestReportBlockOverserviceEffective(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts := time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC)

	// Pre-insert for supplier count.
	insertTestSettlement(t, ctx, s, 100, ts,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		5000, 200, 100, 200, 400, true, 3000)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	settlements := []store.Settlement{{
		EventType:                "settled",
		SupplierOperatorAddress:  "pokt1s1",
		ApplicationAddress:       "pokt1a1",
		ServiceID:                "svc1",
		ClaimedUpokt:             5000,
		NumRelays:                200,
		NumClaimedComputeUnits:   100,
		NumEstimatedComputeUnits: 200,
		EstimatedRelays:          400,
		IsOverserviced:           true,
		EffectiveBurnUpokt:       3000,
	}}

	overservices := []store.OverserviceEvent{{
		ApplicationAddress:      "pokt1a1",
		SupplierOperatorAddress: "pokt1s1",
		ExpectedBurnUpokt:       5000,
		EffectiveBurnUpokt:      3000,
	}}

	err = r.ReportBlock(ctx, 100, ts, settlements, overservices, nil)
	require.NoError(t, err)

	hs := hourStart(ts)
	hourSvc, err := s.GetHourlySummaryService(ctx, hs, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(5000), hourSvc.ClaimedTotalUpokt)
	// Effective should use EffectiveBurnUpokt (3000) not ClaimedUpokt (5000).
	assert.Equal(t, int64(3000), hourSvc.EffectiveTotalUpokt)

	// Network should track overservice count.
	hourNet, err := s.GetHourlySummaryNetwork(ctx, hs)
	require.NoError(t, err)
	assert.Equal(t, int64(1), hourNet.OverserviceCount)
	assert.Equal(t, int64(3000), hourNet.EffectiveTotalUpokt)
}

func TestRecalculateSummariesForRange(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Insert raw settlements spanning 3 hours on the same day.
	ts10 := time.Date(2026, 1, 15, 10, 15, 0, 0, time.UTC)
	ts11 := time.Date(2026, 1, 15, 11, 30, 0, 0, time.UTC)
	ts12 := time.Date(2026, 1, 15, 12, 45, 0, 0, time.UTC)

	insertTestSettlement(t, ctx, s, 100, ts10,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		1000, 10, 5, 10, 20, false, 0)
	insertTestSettlement(t, ctx, s, 101, ts11,
		"settled", "pokt1s2", "pokt1a2", "svc1",
		2000, 20, 10, 20, 40, false, 0)
	insertTestSettlement(t, ctx, s, 102, ts11,
		"expired", "pokt1s1", "pokt1a1", "svc2",
		500, 5, 3, 6, 10, false, 0)
	insertTestSettlement(t, ctx, s, 103, ts12,
		"settled", "pokt1s1", "pokt1a1", "svc1",
		3000, 30, 15, 30, 60, false, 0)

	r, err := NewReporter(ctx, s, zerolog.Nop())
	require.NoError(t, err)

	from := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 15, 13, 0, 0, 0, time.UTC)

	err = r.RecalculateSummariesForRange(ctx, from, to)
	require.NoError(t, err)

	// Verify hourly summaries.
	h10, err := s.GetHourlySummaryService(ctx, hourStart(ts10), "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h10.ClaimsSettled)
	assert.Equal(t, int64(1000), h10.ClaimedTotalUpokt)

	h11svc1, err := s.GetHourlySummaryService(ctx, hourStart(ts11), "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h11svc1.ClaimsSettled)
	assert.Equal(t, int64(2000), h11svc1.ClaimedTotalUpokt)

	h11svc2, err := s.GetHourlySummaryService(ctx, hourStart(ts11), "svc2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h11svc2.ClaimsExpired)
	assert.Equal(t, int64(500), h11svc2.ClaimedTotalUpokt)

	h12, err := s.GetHourlySummaryService(ctx, hourStart(ts12), "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h12.ClaimsSettled)
	assert.Equal(t, int64(3000), h12.ClaimedTotalUpokt)

	// Verify network hourly for hour 11.
	h11net, err := s.GetHourlySummaryNetwork(ctx, hourStart(ts11))
	require.NoError(t, err)
	assert.Equal(t, int64(1), h11net.ClaimsSettled)
	assert.Equal(t, int64(1), h11net.ClaimsExpired)
	assert.Equal(t, int64(2500), h11net.ClaimedTotalUpokt)

	// Verify daily summary for svc1 (accumulates all 3 hours).
	ds := dayStart(ts10)
	dailySvc1, err := s.GetDailySummaryService(ctx, ds, "svc1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), dailySvc1.ClaimsSettled)
	assert.Equal(t, int64(6000), dailySvc1.ClaimedTotalUpokt)

	// Daily network.
	dailyNet, err := s.GetDailySummaryNetwork(ctx, ds)
	require.NoError(t, err)
	assert.Equal(t, int64(3), dailyNet.ClaimsSettled)
	assert.Equal(t, int64(1), dailyNet.ClaimsExpired)
	assert.Equal(t, int64(6500), dailyNet.ClaimedTotalUpokt)
}
