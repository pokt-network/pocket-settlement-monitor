package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"

	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// --- parseCoinAmount tests ---

func TestParseCoinAmount(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"normal amount", "1000upokt", 1000},
		{"zero amount", "0upokt", 0},
		{"large amount", "999999999999upokt", 999999999999},
		{"empty string", "", 0},
		{"invalid string", "abc", 0},
		{"no suffix", "1000", 1000},
		{"only suffix", "upokt", 0},
		{"negative amount", "-500upokt", -500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCoinAmount(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- computeDifficulty tests ---

func TestComputeDifficulty(t *testing.T) {
	tests := []struct {
		name               string
		numRelays          uint64
		numClaimedCU       uint64
		numEstimatedCU     uint64
		expectedMultiplier float64
		expectedEstRelays  int64
	}{
		{
			name:               "division by zero guard",
			numRelays:          100,
			numClaimedCU:       0,
			numEstimatedCU:     0,
			expectedMultiplier: 1.0,
			expectedEstRelays:  100,
		},
		{
			name:               "normal case - 2x difficulty",
			numRelays:          100,
			numClaimedCU:       50,
			numEstimatedCU:     100,
			expectedMultiplier: 2.0,
			expectedEstRelays:  200,
		},
		{
			name:               "base difficulty - no expansion",
			numRelays:          100,
			numClaimedCU:       100,
			numEstimatedCU:     100,
			expectedMultiplier: 1.0,
			expectedEstRelays:  100,
		},
		{
			name:               "high difficulty - 10x",
			numRelays:          50,
			numClaimedCU:       10,
			numEstimatedCU:     100,
			expectedMultiplier: 10.0,
			expectedEstRelays:  500,
		},
		{
			name:               "fractional difficulty",
			numRelays:          100,
			numClaimedCU:       30,
			numEstimatedCU:     100,
			expectedMultiplier: 100.0 / 30.0,
			expectedEstRelays:  333, // int64(float64(100) * (100.0/30.0))
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			multiplier, estRelays := computeDifficulty(tt.numRelays, tt.numClaimedCU, tt.numEstimatedCU)
			assert.InDelta(t, tt.expectedMultiplier, multiplier, 0.0001)
			assert.Equal(t, tt.expectedEstRelays, estRelays)
		})
	}
}

// --- convertSettledEvent tests ---

func TestConvertSettledEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventClaimSettled{
		SupplierOperatorAddress:  "pokt1supplier",
		ApplicationAddress:       "pokt1app",
		ServiceId:                "svc1",
		SessionEndBlockHeight:    100,
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
	}

	settlement := convertSettledEvent(event, 500, ts)

	assert.Equal(t, int64(500), settlement.BlockHeight)
	assert.Equal(t, ts, settlement.BlockTimestamp)
	assert.Equal(t, "settled", settlement.EventType)
	assert.Equal(t, "pokt1supplier", settlement.SupplierOperatorAddress)
	assert.Equal(t, "pokt1app", settlement.ApplicationAddress)
	assert.Equal(t, "svc1", settlement.ServiceID)
	assert.Equal(t, int64(100), settlement.SessionEndBlockHeight)
	assert.Equal(t, int64(5000), settlement.ClaimedUpokt)
	assert.Equal(t, int64(200), settlement.NumRelays)
	assert.Equal(t, int64(100), settlement.NumClaimedComputeUnits)
	assert.Equal(t, int64(200), settlement.NumEstimatedComputeUnits)
	assert.Equal(t, int32(1), settlement.ProofRequirement)
	assert.Equal(t, int32(2), settlement.ClaimProofStatus)

	// Difficulty: 200/100 = 2.0, estimated relays = 200 * 2.0 = 400
	assert.InDelta(t, 2.0, settlement.DifficultyMultiplier, 0.0001)
	assert.Equal(t, int64(400), settlement.EstimatedRelays)

	// Overservice not set by converter (done by correlateOverservice)
	assert.False(t, settlement.IsOverserviced)
}

func TestConvertSettledEvent_RewardDistributions(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventClaimSettled{
		SupplierOperatorAddress: "pokt1supplier",
		ApplicationAddress:      "pokt1app",
		ServiceId:               "svc1",
		ClaimedUpokt:            "5000upokt",
		RewardDistribution: map[string]string{
			"pokt1supplier": "3500upokt",
			"pokt1dao":      "1500upokt",
		},
	}

	_, rewards := convertSettledEventWithRewards(event, 500, ts)

	require.Len(t, rewards, 2)

	// Build a map for order-independent assertions
	rewardMap := make(map[string]int64)
	for _, r := range rewards {
		rewardMap[r.Address] = r.AmountUpokt
	}
	assert.Equal(t, int64(3500), rewardMap["pokt1supplier"])
	assert.Equal(t, int64(1500), rewardMap["pokt1dao"])
}

// --- convertExpiredEvent tests ---

func TestConvertExpiredEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventClaimExpired{
		SupplierOperatorAddress:  "pokt1supplier",
		ApplicationAddress:       "pokt1app",
		ServiceId:                "svc1",
		SessionEndBlockHeight:    100,
		ClaimedUpokt:             "3000upokt",
		NumRelays:                150,
		NumClaimedComputeUnits:   75,
		NumEstimatedComputeUnits: 150,
		ExpirationReason:         tokenomicstypes.ClaimExpirationReason_PROOF_MISSING,
		ClaimProofStatusInt:      3,
	}

	settlement := convertExpiredEvent(event, 500, ts)

	assert.Equal(t, int64(500), settlement.BlockHeight)
	assert.Equal(t, ts, settlement.BlockTimestamp)
	assert.Equal(t, "expired", settlement.EventType)
	assert.Equal(t, "pokt1supplier", settlement.SupplierOperatorAddress)
	assert.Equal(t, "pokt1app", settlement.ApplicationAddress)
	assert.Equal(t, "svc1", settlement.ServiceID)
	assert.Equal(t, int64(100), settlement.SessionEndBlockHeight)
	assert.Equal(t, int64(3000), settlement.ClaimedUpokt)
	assert.Equal(t, int64(150), settlement.NumRelays)
	assert.Equal(t, int64(75), settlement.NumClaimedComputeUnits)
	assert.Equal(t, int64(150), settlement.NumEstimatedComputeUnits)
	assert.Equal(t, "PROOF_MISSING", settlement.ExpirationReason)
	assert.Equal(t, int32(3), settlement.ClaimProofStatus)

	// Difficulty: 150/75 = 2.0, estimated relays = 150 * 2.0 = 300
	assert.InDelta(t, 2.0, settlement.DifficultyMultiplier, 0.0001)
	assert.Equal(t, int64(300), settlement.EstimatedRelays)
}

// --- convertSlashedEvent tests ---

func TestConvertSlashedEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventSupplierSlashed{
		SupplierOperatorAddress: "pokt1supplier",
		ApplicationAddress:      "pokt1app",
		ServiceId:               "svc1",
		SessionEndBlockHeight:   100,
		ProofMissingPenalty:     "1000upokt",
		ClaimProofStatusInt:     2,
	}

	settlement := convertSlashedEvent(event, 500, ts)

	assert.Equal(t, int64(500), settlement.BlockHeight)
	assert.Equal(t, ts, settlement.BlockTimestamp)
	assert.Equal(t, "slashed", settlement.EventType)
	assert.Equal(t, "pokt1supplier", settlement.SupplierOperatorAddress)
	assert.Equal(t, "pokt1app", settlement.ApplicationAddress)
	assert.Equal(t, "svc1", settlement.ServiceID)
	assert.Equal(t, int64(100), settlement.SessionEndBlockHeight)
	assert.Equal(t, int64(1000), settlement.SlashPenaltyUpokt)
	assert.Equal(t, int32(2), settlement.ClaimProofStatus)
}

// --- convertDiscardedEvent tests ---

func TestConvertDiscardedEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventClaimDiscarded{
		SupplierOperatorAddress: "pokt1supplier",
		ApplicationAddress:      "pokt1app",
		ServiceId:               "svc1",
		SessionEndBlockHeight:   100,
		Error:                   "unexpected error during settlement",
		ClaimProofStatusInt:     0,
	}

	settlement := convertDiscardedEvent(event, 500, ts)

	assert.Equal(t, int64(500), settlement.BlockHeight)
	assert.Equal(t, ts, settlement.BlockTimestamp)
	assert.Equal(t, "discarded", settlement.EventType)
	assert.Equal(t, "pokt1supplier", settlement.SupplierOperatorAddress)
	assert.Equal(t, "pokt1app", settlement.ApplicationAddress)
	assert.Equal(t, "svc1", settlement.ServiceID)
	assert.Equal(t, int64(100), settlement.SessionEndBlockHeight)
	assert.Equal(t, "unexpected error during settlement", settlement.ErrorMessage)
	assert.Equal(t, int32(0), settlement.ClaimProofStatus)
}

// --- convertOverserviceEvent tests ---

func TestConvertOverserviceEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventApplicationOverserviced{
		ApplicationAddr:      "pokt1app",
		SupplierOperatorAddr: "pokt1supplier",
		ExpectedBurn:         "5000upokt",
		EffectiveBurn:        "3000upokt",
	}

	osEvent := convertOverserviceEvent(event, 500, ts)

	assert.Equal(t, int64(500), osEvent.BlockHeight)
	assert.Equal(t, ts, osEvent.BlockTimestamp)
	assert.Equal(t, "pokt1app", osEvent.ApplicationAddress)
	assert.Equal(t, "pokt1supplier", osEvent.SupplierOperatorAddress)
	assert.Equal(t, int64(5000), osEvent.ExpectedBurnUpokt)
	assert.Equal(t, int64(3000), osEvent.EffectiveBurnUpokt)
}

// --- convertReimbursementEvent tests ---

func TestConvertReimbursementEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	event := &tokenomicstypes.EventApplicationReimbursementRequest{
		ApplicationAddr:      "pokt1app",
		SupplierOperatorAddr: "pokt1supplier",
		SupplierOwnerAddr:    "pokt1owner",
		ServiceId:            "svc1",
		SessionId:            "session123",
		Amount:               "2000upokt",
	}

	rEvent := convertReimbursementEvent(event, 500, ts)

	assert.Equal(t, int64(500), rEvent.BlockHeight)
	assert.Equal(t, ts, rEvent.BlockTimestamp)
	assert.Equal(t, "pokt1app", rEvent.ApplicationAddress)
	assert.Equal(t, "pokt1supplier", rEvent.SupplierOperatorAddress)
	assert.Equal(t, "pokt1owner", rEvent.SupplierOwnerAddress)
	assert.Equal(t, "svc1", rEvent.ServiceID)
	assert.Equal(t, "session123", rEvent.SessionID)
	assert.Equal(t, int64(2000), rEvent.AmountUpokt)
}

// --- correlateOverservice tests ---

func TestCorrelateOverservice_Matching(t *testing.T) {
	// Create events: 1 overservice + 1 settled for the same supplier+app pair
	events := []subscriber.SettlementEvent{
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app",
				SupplierOperatorAddr: "pokt1supplier",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
	}

	settlements := []store.Settlement{
		{
			BlockHeight:             500,
			EventType:               "settled",
			SupplierOperatorAddress: "pokt1supplier",
			ApplicationAddress:      "pokt1app",
			ClaimedUpokt:            5000,
		},
	}

	count := correlateOverservice(events, settlements)

	assert.Equal(t, 1, count)
	assert.True(t, settlements[0].IsOverserviced)
	assert.Equal(t, int64(3000), settlements[0].EffectiveBurnUpokt)
	assert.Equal(t, int64(2000), settlements[0].OverserviceDiffUpokt) // 5000 - 3000
}

func TestCorrelateOverservice_NoMatch(t *testing.T) {
	// Overservice for a different supplier+app pair
	events := []subscriber.SettlementEvent{
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app_other",
				SupplierOperatorAddr: "pokt1supplier_other",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
	}

	settlements := []store.Settlement{
		{
			BlockHeight:             500,
			EventType:               "settled",
			SupplierOperatorAddress: "pokt1supplier",
			ApplicationAddress:      "pokt1app",
			ClaimedUpokt:            5000,
		},
	}

	count := correlateOverservice(events, settlements)

	assert.Equal(t, 0, count)
	assert.False(t, settlements[0].IsOverserviced)
	assert.Equal(t, int64(0), settlements[0].EffectiveBurnUpokt)
	assert.Equal(t, int64(0), settlements[0].OverserviceDiffUpokt)
}

func TestCorrelateOverservice_OnlySettledCorrelated(t *testing.T) {
	// Overservice should only correlate with "settled" events, not "expired"
	events := []subscriber.SettlementEvent{
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app",
				SupplierOperatorAddr: "pokt1supplier",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
	}

	settlements := []store.Settlement{
		{
			BlockHeight:             500,
			EventType:               "expired",
			SupplierOperatorAddress: "pokt1supplier",
			ApplicationAddress:      "pokt1app",
			ClaimedUpokt:            5000,
		},
	}

	count := correlateOverservice(events, settlements)

	assert.Equal(t, 0, count)
	assert.False(t, settlements[0].IsOverserviced)
}

func TestCorrelateOverservice_NoOverserviceEvents(t *testing.T) {
	// No overservice events in the block
	events := []subscriber.SettlementEvent{
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventClaimSettled",
			Event: &tokenomicstypes.EventClaimSettled{
				SupplierOperatorAddress: "pokt1supplier",
				ApplicationAddress:      "pokt1app",
			},
		},
	}

	settlements := []store.Settlement{
		{
			BlockHeight:             500,
			EventType:               "settled",
			SupplierOperatorAddress: "pokt1supplier",
			ApplicationAddress:      "pokt1app",
			ClaimedUpokt:            5000,
		},
	}

	count := correlateOverservice(events, settlements)

	assert.Equal(t, 0, count)
	assert.False(t, settlements[0].IsOverserviced)
}

func TestCorrelateOverservice_MultipleCorrelations(t *testing.T) {
	// Two different supplier+app pairs with overservice
	events := []subscriber.SettlementEvent{
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app1",
				SupplierOperatorAddr: "pokt1supplier1",
				ExpectedBurn:         "5000upokt",
				EffectiveBurn:        "3000upokt",
			},
		},
		{
			Height:    500,
			EventType: "pocket.tokenomics.EventApplicationOverserviced",
			Event: &tokenomicstypes.EventApplicationOverserviced{
				ApplicationAddr:      "pokt1app2",
				SupplierOperatorAddr: "pokt1supplier2",
				ExpectedBurn:         "8000upokt",
				EffectiveBurn:        "6000upokt",
			},
		},
	}

	settlements := []store.Settlement{
		{
			BlockHeight:             500,
			EventType:               "settled",
			SupplierOperatorAddress: "pokt1supplier1",
			ApplicationAddress:      "pokt1app1",
			ClaimedUpokt:            5000,
		},
		{
			BlockHeight:             500,
			EventType:               "settled",
			SupplierOperatorAddress: "pokt1supplier2",
			ApplicationAddress:      "pokt1app2",
			ClaimedUpokt:            8000,
		},
	}

	count := correlateOverservice(events, settlements)

	assert.Equal(t, 2, count)
	assert.True(t, settlements[0].IsOverserviced)
	assert.Equal(t, int64(3000), settlements[0].EffectiveBurnUpokt)
	assert.Equal(t, int64(2000), settlements[0].OverserviceDiffUpokt)
	assert.True(t, settlements[1].IsOverserviced)
	assert.Equal(t, int64(6000), settlements[1].EffectiveBurnUpokt)
	assert.Equal(t, int64(2000), settlements[1].OverserviceDiffUpokt)
}
