package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- Settlement filtered query tests ---

func TestQuerySettlementsFiltered_NoFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert 3 settlements across 2 blocks.
	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s2 := makeSettlement(100, "expired", "pokt1supp2", "pokt1app2", 96)
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2}, nil, nil, nil))

	s3 := makeSettlement(200, "settled", "pokt1supp3", "pokt1app3", 195)
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s3}, nil, nil, nil))

	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{})
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Ordered by block_height DESC, id DESC — block 200 first, then block 100 in reverse id order.
	require.Equal(t, int64(200), results[0].BlockHeight)
	require.Equal(t, int64(100), results[1].BlockHeight)
	require.Equal(t, int64(100), results[2].BlockHeight)
}

func TestQuerySettlementsFiltered_ByEventType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s2 := makeSettlement(100, "expired", "pokt1supp2", "pokt1app2", 96)
	block := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block, []Settlement{s1, s2}, nil, nil, nil))

	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{EventType: "settled"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "settled", results[0].EventType)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
}

func TestQuerySettlementsFiltered_BySupplier(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s2 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app2", 96)
	s3 := makeSettlement(200, "settled", "pokt1supp1", "pokt1app3", 195)
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2}, nil, nil, nil))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s3}, nil, nil, nil))

	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{
		SupplierOperatorAddress: "pokt1supp1",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, "pokt1supp1", r.SupplierOperatorAddress)
	}
}

func TestQuerySettlementsFiltered_ByService(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.ServiceID = "svc_alpha"
	s2 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app2", 96)
	s2.ServiceID = "svc_beta"
	block := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block, []Settlement{s1, s2}, nil, nil, nil))

	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{ServiceID: "svc_alpha"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "svc_alpha", results[0].ServiceID)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
}

func TestQuerySettlementsFiltered_ByHeightRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	block1 := makeProcessedBlock(100, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1}, nil, nil, nil))

	s2 := makeSettlement(200, "settled", "pokt1supp2", "pokt1app2", 195)
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s2}, nil, nil, nil))

	s3 := makeSettlement(300, "settled", "pokt1supp3", "pokt1app3", 295)
	block3 := makeProcessedBlock(300, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block3, []Settlement{s3}, nil, nil, nil))

	// Select only the middle block (FromHeight=200, ToHeight=200).
	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{
		FromHeight: 200,
		ToHeight:   200,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, int64(200), results[0].BlockHeight)

	// Select range 100-200 (inclusive on both ends).
	results, err = s.QuerySettlementsFiltered(ctx, SettlementFilters{
		FromHeight: 100,
		ToHeight:   200,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// Ordered DESC: 200 first, then 100.
	require.Equal(t, int64(200), results[0].BlockHeight)
	require.Equal(t, int64(100), results[1].BlockHeight)
}

func TestQuerySettlementsFiltered_ByTimeRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)
	hour3 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.BlockTimestamp = hour1
	block1 := ProcessedBlock{Height: 100, BlockTimestamp: hour1, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1}, nil, nil, nil))

	s2 := makeSettlement(200, "settled", "pokt1supp2", "pokt1app2", 195)
	s2.BlockTimestamp = hour2
	block2 := ProcessedBlock{Height: 200, BlockTimestamp: hour2, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s2}, nil, nil, nil))

	s3 := makeSettlement(300, "settled", "pokt1supp3", "pokt1app3", 295)
	s3.BlockTimestamp = hour3
	block3 := ProcessedBlock{Height: 300, BlockTimestamp: hour3, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block3, []Settlement{s3}, nil, nil, nil))

	// Half-open interval [hour1, hour3) should return hour1 and hour2 only.
	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{
		FromTime: hour1,
		ToTime:   hour3,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// Ordered by height DESC: 200 first, then 100.
	require.Equal(t, int64(200), results[0].BlockHeight)
	require.Equal(t, int64(100), results[1].BlockHeight)
}

func TestQuerySettlementsFiltered_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s2 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app2", 96)
	s3 := makeSettlement(200, "settled", "pokt1supp3", "pokt1app3", 195)
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2}, nil, nil, nil))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s3}, nil, nil, nil))

	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{Limit: 2})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// Most recent first (height 200), then one from height 100.
	require.Equal(t, int64(200), results[0].BlockHeight)
}

func TestQuerySettlementsFiltered_CombinedFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)

	// Block 100: settled by supp1 for svc1 at hour1.
	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	s1.ServiceID = "svc1"
	s1.BlockTimestamp = hour1
	// Block 100: expired by supp1 for svc1 at hour1.
	s2 := makeSettlement(100, "expired", "pokt1supp1", "pokt1app2", 96)
	s2.ServiceID = "svc1"
	s2.BlockTimestamp = hour1
	// Block 100: settled by supp2 for svc1 at hour1.
	s3 := makeSettlement(100, "settled", "pokt1supp2", "pokt1app3", 97)
	s3.ServiceID = "svc1"
	s3.BlockTimestamp = hour1

	block1 := ProcessedBlock{Height: 100, BlockTimestamp: hour1, EventCount: 3, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block1, []Settlement{s1, s2, s3}, nil, nil, nil))

	// Block 200: settled by supp1 for svc2 at hour2.
	s4 := makeSettlement(200, "settled", "pokt1supp1", "pokt1app4", 195)
	s4.ServiceID = "svc2"
	s4.BlockTimestamp = hour2
	block2 := ProcessedBlock{Height: 200, BlockTimestamp: hour2, EventCount: 1, Source: "live"}
	require.NoError(t, s.InsertBlockEvents(ctx, block2, []Settlement{s4}, nil, nil, nil))

	// Combine: EventType=settled + Supplier=supp1 + ServiceID=svc1.
	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{
		EventType:               "settled",
		SupplierOperatorAddress: "pokt1supp1",
		ServiceID:               "svc1",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "settled", results[0].EventType)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
	require.Equal(t, "svc1", results[0].ServiceID)
	require.Equal(t, int64(100), results[0].BlockHeight)
}

func TestQuerySettlementsFiltered_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s1 := makeSettlement(100, "settled", "pokt1supp1", "pokt1app1", 95)
	block := makeProcessedBlock(100, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block, []Settlement{s1}, nil, nil, nil))

	// Filter by a supplier that does not exist.
	results, err := s.QuerySettlementsFiltered(ctx, SettlementFilters{
		SupplierOperatorAddress: "pokt1nonexistent",
	})
	require.NoError(t, err)
	require.Empty(t, results)
}

// --- Overservice filtered query tests ---

func TestQueryOverserviceFiltered_NoFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	oe2 := makeOverserviceEvent(100, "pokt1supp2", "pokt1app2")
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, []OverserviceEvent{oe1, oe2}, nil))

	oe3 := makeOverserviceEvent(200, "pokt1supp3", "pokt1app3")
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, []OverserviceEvent{oe3}, nil))

	results, err := s.QueryOverserviceFiltered(ctx, OverserviceFilters{})
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Ordered by block_height DESC.
	require.Equal(t, int64(200), results[0].BlockHeight)
}

func TestQueryOverserviceFiltered_BySupplier(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	oe2 := makeOverserviceEvent(100, "pokt1supp2", "pokt1app2")
	block := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block, nil, nil, []OverserviceEvent{oe1, oe2}, nil))

	results, err := s.QueryOverserviceFiltered(ctx, OverserviceFilters{
		SupplierOperatorAddress: "pokt1supp1",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
	require.Equal(t, "pokt1app1", results[0].ApplicationAddress)
}

func TestQueryOverserviceFiltered_ByApplication(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	oe2 := makeOverserviceEvent(100, "pokt1supp2", "pokt1app2")
	oe3 := makeOverserviceEvent(200, "pokt1supp3", "pokt1app1")
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, []OverserviceEvent{oe1, oe2}, nil))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, []OverserviceEvent{oe3}, nil))

	results, err := s.QueryOverserviceFiltered(ctx, OverserviceFilters{
		ApplicationAddress: "pokt1app1",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, "pokt1app1", r.ApplicationAddress)
	}
}

func TestQueryOverserviceFiltered_ByHeightRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	block1 := makeProcessedBlock(100, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, []OverserviceEvent{oe1}, nil))

	oe2 := makeOverserviceEvent(200, "pokt1supp2", "pokt1app2")
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, []OverserviceEvent{oe2}, nil))

	oe3 := makeOverserviceEvent(300, "pokt1supp3", "pokt1app3")
	block3 := makeProcessedBlock(300, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block3, nil, nil, []OverserviceEvent{oe3}, nil))

	results, err := s.QueryOverserviceFiltered(ctx, OverserviceFilters{
		FromHeight: 150,
		ToHeight:   250,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, int64(200), results[0].BlockHeight)
}

func TestQueryOverserviceFiltered_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oe1 := makeOverserviceEvent(100, "pokt1supp1", "pokt1app1")
	oe2 := makeOverserviceEvent(200, "pokt1supp2", "pokt1app2")
	oe3 := makeOverserviceEvent(300, "pokt1supp3", "pokt1app3")
	block1 := makeProcessedBlock(100, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, []OverserviceEvent{oe1}, nil))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, []OverserviceEvent{oe2}, nil))
	block3 := makeProcessedBlock(300, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block3, nil, nil, []OverserviceEvent{oe3}, nil))

	results, err := s.QueryOverserviceFiltered(ctx, OverserviceFilters{Limit: 2})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// Most recent first.
	require.Equal(t, int64(300), results[0].BlockHeight)
	require.Equal(t, int64(200), results[1].BlockHeight)
}

// --- Reimbursement filtered query tests ---

func TestQueryReimbursementsFiltered_NoFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.SessionID = "sess1"
	re2 := makeReimbursementEvent(100, "pokt1supp2", "pokt1app2")
	re2.SessionID = "sess2"
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, nil, []ReimbursementEvent{re1, re2}))

	re3 := makeReimbursementEvent(200, "pokt1supp3", "pokt1app3")
	re3.SessionID = "sess3"
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, nil, []ReimbursementEvent{re3}))

	results, err := s.QueryReimbursementsFiltered(ctx, ReimbursementFilters{})
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Ordered by block_height DESC.
	require.Equal(t, int64(200), results[0].BlockHeight)
}

func TestQueryReimbursementsFiltered_BySupplier(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.SessionID = "sess1"
	re2 := makeReimbursementEvent(100, "pokt1supp2", "pokt1app2")
	re2.SessionID = "sess2"
	block := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block, nil, nil, nil, []ReimbursementEvent{re1, re2}))

	results, err := s.QueryReimbursementsFiltered(ctx, ReimbursementFilters{
		SupplierOperatorAddress: "pokt1supp1",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
}

func TestQueryReimbursementsFiltered_ByService(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.ServiceID = "svc_alpha"
	re1.SessionID = "sess1"
	re2 := makeReimbursementEvent(100, "pokt1supp2", "pokt1app2")
	re2.ServiceID = "svc_beta"
	re2.SessionID = "sess2"
	block := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block, nil, nil, nil, []ReimbursementEvent{re1, re2}))

	results, err := s.QueryReimbursementsFiltered(ctx, ReimbursementFilters{
		ServiceID: "svc_alpha",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "svc_alpha", results[0].ServiceID)
	require.Equal(t, "pokt1supp1", results[0].SupplierOperatorAddress)
}

func TestQueryReimbursementsFiltered_ByApplication(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.SessionID = "sess1"
	re2 := makeReimbursementEvent(100, "pokt1supp2", "pokt1app2")
	re2.SessionID = "sess2"
	re3 := makeReimbursementEvent(200, "pokt1supp3", "pokt1app1")
	re3.SessionID = "sess3"
	block1 := makeProcessedBlock(100, "live", 2)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, nil, []ReimbursementEvent{re1, re2}))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, nil, []ReimbursementEvent{re3}))

	results, err := s.QueryReimbursementsFiltered(ctx, ReimbursementFilters{
		ApplicationAddress: "pokt1app1",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, "pokt1app1", r.ApplicationAddress)
	}
}

func TestQueryReimbursementsFiltered_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	re1 := makeReimbursementEvent(100, "pokt1supp1", "pokt1app1")
	re1.SessionID = "sess1"
	re2 := makeReimbursementEvent(200, "pokt1supp2", "pokt1app2")
	re2.SessionID = "sess2"
	re3 := makeReimbursementEvent(300, "pokt1supp3", "pokt1app3")
	re3.SessionID = "sess3"
	block1 := makeProcessedBlock(100, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block1, nil, nil, nil, []ReimbursementEvent{re1}))
	block2 := makeProcessedBlock(200, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block2, nil, nil, nil, []ReimbursementEvent{re2}))
	block3 := makeProcessedBlock(300, "live", 1)
	require.NoError(t, s.InsertBlockEvents(ctx, block3, nil, nil, nil, []ReimbursementEvent{re3}))

	results, err := s.QueryReimbursementsFiltered(ctx, ReimbursementFilters{Limit: 1})
	require.NoError(t, err)
	require.Len(t, results, 1)
	// Most recent first.
	require.Equal(t, int64(300), results[0].BlockHeight)
}

// --- Hourly summary filtered query tests ---

func TestQueryHourlySummariesFiltered_Network(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)

	// Insert network summaries.
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour1,
		ClaimsSettled: 10,
		NumRelays:     500,
	}))
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour2,
		ClaimsSettled: 20,
		NumRelays:     1000,
	}))

	// Empty ServiceID should query network table.
	results, err := s.QueryHourlySummariesFiltered(ctx, SummaryFilters{})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results should have ServiceID="network" set by the query function.
	for _, r := range results {
		require.Equal(t, "network", r.ServiceID)
	}

	// Ordered by hour_start DESC: hour2 first.
	require.Equal(t, hour2, results[0].HourStart)
	require.Equal(t, int64(20), results[0].ClaimsSettled)
	require.Equal(t, hour1, results[1].HourStart)
	require.Equal(t, int64(10), results[1].ClaimsSettled)
}

func TestQueryHourlySummariesFiltered_Service(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)

	// Insert service summaries for two services.
	require.NoError(t, s.UpsertHourlySummaryService(ctx, HourlySummaryService{
		HourStart:     hour1,
		ServiceID:     "svc1",
		ClaimsSettled: 5,
		NumRelays:     250,
	}))
	require.NoError(t, s.UpsertHourlySummaryService(ctx, HourlySummaryService{
		HourStart:     hour1,
		ServiceID:     "svc2",
		ClaimsSettled: 8,
		NumRelays:     400,
	}))
	require.NoError(t, s.UpsertHourlySummaryService(ctx, HourlySummaryService{
		HourStart:     hour2,
		ServiceID:     "svc1",
		ClaimsSettled: 12,
		NumRelays:     600,
	}))

	// Query for svc1 only.
	results, err := s.QueryHourlySummariesFiltered(ctx, SummaryFilters{ServiceID: "svc1"})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Ordered by hour_start DESC.
	require.Equal(t, hour2, results[0].HourStart)
	require.Equal(t, "svc1", results[0].ServiceID)
	require.Equal(t, int64(12), results[0].ClaimsSettled)
	require.Equal(t, hour1, results[1].HourStart)
	require.Equal(t, "svc1", results[1].ServiceID)
	require.Equal(t, int64(5), results[1].ClaimsSettled)
}

func TestQueryHourlySummariesFiltered_TimeRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)
	hour3 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour1,
		ClaimsSettled: 10,
	}))
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour2,
		ClaimsSettled: 20,
	}))
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour3,
		ClaimsSettled: 30,
	}))

	// Half-open interval [hour1, hour3) should return hour1 and hour2.
	results, err := s.QueryHourlySummariesFiltered(ctx, SummaryFilters{
		FromTime: hour1,
		ToTime:   hour3,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// DESC order: hour2 first.
	require.Equal(t, hour2, results[0].HourStart)
	require.Equal(t, hour1, results[1].HourStart)
}

func TestQueryHourlySummariesFiltered_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	hour1 := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)
	hour3 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour1,
		ClaimsSettled: 10,
	}))
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour2,
		ClaimsSettled: 20,
	}))
	require.NoError(t, s.UpsertHourlySummaryNetwork(ctx, HourlySummaryNetwork{
		HourStart:     hour3,
		ClaimsSettled: 30,
	}))

	results, err := s.QueryHourlySummariesFiltered(ctx, SummaryFilters{Limit: 1})
	require.NoError(t, err)
	require.Len(t, results, 1)
	// Most recent first.
	require.Equal(t, hour3, results[0].HourStart)
	require.Equal(t, int64(30), results[0].ClaimsSettled)
}

// --- Daily summary filtered query tests ---

func TestQueryDailySummariesFiltered_Network(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	day1 := time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, DailySummaryNetwork{
		DayDate:       day1,
		ClaimsSettled: 100,
		NumRelays:     50000,
	}))
	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, DailySummaryNetwork{
		DayDate:       day2,
		ClaimsSettled: 200,
		NumRelays:     100000,
	}))

	// Empty ServiceID queries network table.
	results, err := s.QueryDailySummariesFiltered(ctx, SummaryFilters{})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results should have ServiceID="network".
	for _, r := range results {
		require.Equal(t, "network", r.ServiceID)
	}

	// Ordered by day_date DESC: day2 first.
	require.Equal(t, day2, results[0].DayDate)
	require.Equal(t, int64(200), results[0].ClaimsSettled)
	require.Equal(t, day1, results[1].DayDate)
	require.Equal(t, int64(100), results[1].ClaimsSettled)
}

func TestQueryDailySummariesFiltered_Service(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	day1 := time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// Insert service summaries for two services.
	require.NoError(t, s.UpsertDailySummaryService(ctx, DailySummaryService{
		DayDate:       day1,
		ServiceID:     "svc1",
		ClaimsSettled: 50,
		NumRelays:     25000,
	}))
	require.NoError(t, s.UpsertDailySummaryService(ctx, DailySummaryService{
		DayDate:       day1,
		ServiceID:     "svc2",
		ClaimsSettled: 30,
		NumRelays:     15000,
	}))
	require.NoError(t, s.UpsertDailySummaryService(ctx, DailySummaryService{
		DayDate:       day2,
		ServiceID:     "svc1",
		ClaimsSettled: 75,
		NumRelays:     37500,
	}))

	// Query for svc1 only.
	results, err := s.QueryDailySummariesFiltered(ctx, SummaryFilters{ServiceID: "svc1"})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Ordered by day_date DESC.
	require.Equal(t, day2, results[0].DayDate)
	require.Equal(t, "svc1", results[0].ServiceID)
	require.Equal(t, int64(75), results[0].ClaimsSettled)
	require.Equal(t, day1, results[1].DayDate)
	require.Equal(t, "svc1", results[1].ServiceID)
	require.Equal(t, int64(50), results[1].ClaimsSettled)
}

func TestQueryDailySummariesFiltered_TimeRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	day1 := time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, DailySummaryNetwork{
		DayDate:       day1,
		ClaimsSettled: 100,
	}))
	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, DailySummaryNetwork{
		DayDate:       day2,
		ClaimsSettled: 200,
	}))
	require.NoError(t, s.UpsertDailySummaryNetwork(ctx, DailySummaryNetwork{
		DayDate:       day3,
		ClaimsSettled: 300,
	}))

	// Half-open interval [day1, day3) should return day1 and day2.
	results, err := s.QueryDailySummariesFiltered(ctx, SummaryFilters{
		FromTime: day1,
		ToTime:   day3,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	// DESC order: day2 first.
	require.Equal(t, day2, results[0].DayDate)
	require.Equal(t, int64(200), results[0].ClaimsSettled)
	require.Equal(t, day1, results[1].DayDate)
	require.Equal(t, int64(100), results[1].ClaimsSettled)
}
