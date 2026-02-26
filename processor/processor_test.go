package processor

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"

	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// newTestStore creates an in-memory SQLiteStore for testing.
func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:", 0, zerolog.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

// newTestMetrics creates a fresh Metrics instance on a new registry for test isolation.
func newTestMetrics(t *testing.T) (*metrics.Metrics, *prometheus.Registry) {
	t.Helper()
	registry := prometheus.NewRegistry()
	labels := &metrics.LabelConfig{
		IncludeSupplier:    true,
		IncludeService:     true,
		IncludeApplication: true,
	}
	m := metrics.NewMetrics(registry, labels)
	return m, registry
}

// newTestProcessor creates a Processor wired to an in-memory store and fresh metrics.
func newTestProcessor(t *testing.T) (*Processor, *store.SQLiteStore, *prometheus.Registry) {
	t.Helper()
	s := newTestStore(t)
	m, reg := newTestMetrics(t)
	filter := NewSupplierFilter(nil, zerolog.Nop()) // monitor-all mode
	p := NewProcessor(s, m, filter, zerolog.Nop())
	return p, s, reg
}

// getCounterValue reads a counter value from the registry for assertion.
func getCounterValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				if m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// getGaugeValue reads a gauge value from the registry.
func getGaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				if m.GetGauge() != nil {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}

// getCounterVecValue reads a counter value with specific labels from the registry.
func getCounterVecValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				if matchLabels(m.GetLabel(), labels) && m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// matchLabels checks if a metric's labels match the expected labels.
func matchLabels(metricLabels []*io_prometheus_client.LabelPair, expected map[string]string) bool {
	labelMap := make(map[string]string)
	for _, lp := range metricLabels {
		labelMap[lp.GetName()] = lp.GetValue()
	}
	for k, v := range expected {
		if labelMap[k] != v {
			return false
		}
	}
	return true
}

func TestProcessBlock_SettledEvent(t *testing.T) {
	p, s, reg := newTestProcessor(t)
	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress:  "pokt1supplier",
				ApplicationAddress:       "pokt1app",
				ServiceId:                "svc1",
				SessionEndBlockHeight:    90,
				ClaimedUpokt:             "5000upokt",
				NumRelays:                200,
				NumClaimedComputeUnits:   100,
				NumEstimatedComputeUnits: 200,
				ProofRequirementInt:      1,
				ClaimProofStatusInt:      2,
				RewardDistribution: map[string]string{
					"pokt1supplier": "3500upokt",
					"pokt1dao":      "1500upokt",
				},
			},
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, true)
	require.NoError(t, err)

	// Verify stored in DB.
	var count int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements WHERE block_height = 100").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify reward distributions stored.
	var rewardCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM reward_distributions").Scan(&rewardCount)
	require.NoError(t, err)
	assert.Equal(t, 2, rewardCount)

	// Verify processed block record.
	var source string
	err = s.DB().QueryRowContext(ctx, "SELECT source FROM processed_blocks WHERE height = 100").Scan(&source)
	require.NoError(t, err)
	assert.Equal(t, "live", source)

	// Verify metrics incremented (isLive=true).
	claimsSettled := getCounterVecValue(t, reg, "psm_claims_settled_total", map[string]string{
		"event_type": "settled",
		"supplier":   "pokt1supplier",
		"service":    "svc1",
	})
	assert.Equal(t, 1.0, claimsSettled)

	// Note: blocks_processed_total is now incremented by the subscriber's OnBlock
	// callback (for every block, not just settlement blocks), so the processor
	// no longer increments it.

	// Verify gauge always updates.
	lastHeight := getGaugeValue(t, reg, "psm_last_processed_height")
	assert.Equal(t, 100.0, lastHeight)
}

func TestProcessBlock_LiveOnlyMetrics(t *testing.T) {
	p, s, reg := newTestProcessor(t)
	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress:  "pokt1supplier",
				ApplicationAddress:       "pokt1app",
				ServiceId:                "svc1",
				ClaimedUpokt:             "5000upokt",
				NumRelays:                200,
				NumClaimedComputeUnits:   100,
				NumEstimatedComputeUnits: 200,
			},
		},
	}

	// Process with isLive=false (backfill).
	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, false)
	require.NoError(t, err)

	// Verify stored in DB (always stored).
	var count int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements WHERE block_height = 100").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify source is "backfill".
	var source string
	err = s.DB().QueryRowContext(ctx, "SELECT source FROM processed_blocks WHERE height = 100").Scan(&source)
	require.NoError(t, err)
	assert.Equal(t, "backfill", source)

	// Verify counters NOT incremented (isLive=false).
	claimsSettled := getCounterVecValue(t, reg, "psm_claims_settled_total", map[string]string{
		"event_type": "settled",
	})
	assert.Equal(t, 0.0, claimsSettled)

	blocksProcessed := getCounterValue(t, reg, "psm_blocks_processed_total")
	assert.Equal(t, 0.0, blocksProcessed)

	// Verify gauge STILL updates (gauges always update regardless of isLive).
	lastHeight := getGaugeValue(t, reg, "psm_last_processed_height")
	assert.Equal(t, 100.0, lastHeight)
}

func TestProcessBlock_SupplierFilter(t *testing.T) {
	s := newTestStore(t)
	m, _ := newTestMetrics(t)
	// Filter: only pokt1supplier_a passes.
	filter := NewSupplierFilter([]string{"pokt1supplier_a"}, zerolog.Nop())
	p := NewProcessor(s, m, filter, zerolog.Nop())

	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress: "pokt1supplier_a", // matches
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ClaimedUpokt:            "5000upokt",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress: "pokt1supplier_b", // does NOT match
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ClaimedUpokt:            "3000upokt",
			},
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, true)
	require.NoError(t, err)

	// Only 1 event should be stored (the matching one).
	var count int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements WHERE block_height = 100").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify the stored one is supplier_a.
	var addr string
	err = s.DB().QueryRowContext(ctx, "SELECT supplier_operator_address FROM settlements WHERE block_height = 100").Scan(&addr)
	require.NoError(t, err)
	assert.Equal(t, "pokt1supplier_a", addr)
}

func TestProcessBlock_OverserviceCorrelation(t *testing.T) {
	p, s, _ := newTestProcessor(t)
	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ClaimedUpokt:            "5000upokt",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app",
				SupplierOperatorAddr: "pokt1supplier",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, true)
	require.NoError(t, err)

	// Verify settlement is marked as overserviced.
	var isOverserviced int
	var effectiveBurn, overserviceDiff int64
	err = s.DB().QueryRowContext(ctx,
		"SELECT is_overserviced, effective_burn_upokt, overservice_diff_upokt FROM settlements WHERE block_height = 100",
	).Scan(&isOverserviced, &effectiveBurn, &overserviceDiff)
	require.NoError(t, err)
	assert.Equal(t, 1, isOverserviced)
	assert.Equal(t, int64(3000), effectiveBurn)
	assert.Equal(t, int64(2000), overserviceDiff)

	// Verify overservice event also stored.
	var osCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events WHERE block_height = 100").Scan(&osCount)
	require.NoError(t, err)
	assert.Equal(t, 1, osCount)
}

func TestProcessBlock_BlockSummary(t *testing.T) {
	s := newTestStore(t)
	m, _ := newTestMetrics(t)
	filter := NewSupplierFilter(nil, zerolog.Nop())

	// Capture log output.
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.InfoLevel)
	p := NewProcessor(s, m, filter, logger)

	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress:  "pokt1supplier",
				ApplicationAddress:       "pokt1app",
				ServiceId:                "svc1",
				ClaimedUpokt:             "5000upokt",
				NumRelays:                200,
				NumClaimedComputeUnits:   100,
				NumEstimatedComputeUnits: 200,
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimExpired",
			Event: &tokenomicstypes.EventClaimExpired{
				SupplierOperatorAddress:  "pokt1supplier",
				ApplicationAddress:       "pokt1app",
				ServiceId:                "svc1",
				ClaimedUpokt:             "3000upokt",
				NumRelays:                100,
				NumClaimedComputeUnits:   50,
				NumEstimatedComputeUnits: 100,
				ExpirationReason:         tokenomicstypes.ClaimExpirationReason_PROOF_MISSING,
			},
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, true)
	require.NoError(t, err)

	// Verify log output contains expected summary fields.
	logOutput := buf.String()
	assert.Contains(t, logOutput, "block processed")
	assert.Contains(t, logOutput, `"height":100`)
	assert.Contains(t, logOutput, `"events":2`)
	assert.Contains(t, logOutput, `"settled_upokt":5000`)
	assert.Contains(t, logOutput, `"expired_upokt":3000`)
	assert.Contains(t, logOutput, `"settled_relays":200`)
	assert.Contains(t, logOutput, `"expired_relays":100`)
	assert.Contains(t, logOutput, `"settled_compute_units":100`)
	assert.Contains(t, logOutput, `"expired_compute_units":50`)
}

func TestProcessBlock_AllEventTypes(t *testing.T) {
	p, s, _ := newTestProcessor(t)
	ctx := context.Background()

	events := []subscriber.SettlementEvent{
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ClaimedUpokt:            "5000upokt",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimExpired",
			Event: &tokenomicstypes.EventClaimExpired{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ClaimedUpokt:            "3000upokt",
				ExpirationReason:        tokenomicstypes.ClaimExpirationReason_PROOF_MISSING,
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventSupplierSlashed",
			Event: &tokenomicstypes.EventSupplierSlashed{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				ProofMissingPenalty:     "1000upokt",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventClaimDiscarded",
			Event: &tokenomicstypes.EventClaimDiscarded{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
				ServiceId:               "svc1",
				Error:                   "test error",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app",
				SupplierOperatorAddr: "pokt1supplier",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
		{
			Height:    100,
			EventType: "pokt.tokenomics.EventApplicationReimbursementRequest",
			Event: &tokenomicstypes.EventApplicationReimbursementRequest{
				ApplicationAddr:      "pokt1app",
				SupplierOperatorAddr: "pokt1supplier",
				SupplierOwnerAddr:    "pokt1owner",
				ServiceId:            "svc1",
				SessionId:            "session123",
				Amount:               "2000upokt",
			},
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := p.ProcessBlock(ctx, 100, testTime, events, true)
	require.NoError(t, err)

	// Verify settlements table: 4 settlement event types.
	var settlementCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM settlements WHERE block_height = 100").Scan(&settlementCount)
	require.NoError(t, err)
	assert.Equal(t, 4, settlementCount)

	// Verify each event type is present.
	for _, eventType := range []string{"settled", "expired", "slashed", "discarded"} {
		var count int
		err = s.DB().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM settlements WHERE block_height = 100 AND event_type = ?", eventType,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "expected 1 %s settlement", eventType)
	}

	// Verify overservice events table.
	var osCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM overservice_events WHERE block_height = 100").Scan(&osCount)
	require.NoError(t, err)
	assert.Equal(t, 1, osCount)

	// Verify reimbursement events table.
	var reimbCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM reimbursement_events WHERE block_height = 100").Scan(&reimbCount)
	require.NoError(t, err)
	assert.Equal(t, 1, reimbCount)

	// Verify reimbursement fields.
	var (
		appAddr      string
		supplierAddr string
		ownerAddr    string
		serviceID    string
		sessionID    string
		amount       int64
	)
	err = s.DB().QueryRowContext(ctx,
		"SELECT application_address, supplier_operator_address, supplier_owner_address, service_id, session_id, amount_upokt FROM reimbursement_events WHERE block_height = 100",
	).Scan(&appAddr, &supplierAddr, &ownerAddr, &serviceID, &sessionID, &amount)
	require.NoError(t, err)
	assert.Equal(t, "pokt1app", appAddr)
	assert.Equal(t, "pokt1supplier", supplierAddr)
	assert.Equal(t, "pokt1owner", ownerAddr)
	assert.Equal(t, "svc1", serviceID)
	assert.Equal(t, "session123", sessionID)
	assert.Equal(t, int64(2000), amount)

	// Verify processed block record.
	var pbHeight int64
	var pbSource string
	err = s.DB().QueryRowContext(ctx, "SELECT height, source FROM processed_blocks WHERE height = 100").Scan(&pbHeight, &pbSource)
	require.NoError(t, err)
	assert.Equal(t, int64(100), pbHeight)
	assert.Equal(t, "live", pbSource)

	// Verify the settled claim is correlated with overservice.
	var isOverserviced int
	err = s.DB().QueryRowContext(ctx,
		"SELECT is_overserviced FROM settlements WHERE block_height = 100 AND event_type = 'settled'",
	).Scan(&isOverserviced)
	if !errors.Is(err, sql.ErrNoRows) {
		require.NoError(t, err)
		assert.Equal(t, 1, isOverserviced)
	}
}
