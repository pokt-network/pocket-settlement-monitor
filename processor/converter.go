package processor

import (
	"strconv"
	"strings"
	"time"

	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"

	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// parseCoinAmount extracts the integer amount from a coin string like "1000upokt".
// Returns 0 if the string is empty or unparseable.
func parseCoinAmount(coinStr string) int64 {
	if coinStr == "" {
		return 0
	}
	// Strip "upokt" suffix
	amountStr := strings.TrimSuffix(coinStr, "upokt")
	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil {
		return 0
	}
	return amount
}

// computeDifficulty calculates the difficulty multiplier and estimated relays.
// Guard: if numClaimedCU == 0, multiplier defaults to 1.0 and estimatedRelays = numRelays.
func computeDifficulty(numRelays, numClaimedCU, numEstimatedCU uint64) (float64, int64) {
	if numClaimedCU == 0 {
		return 1.0, int64(numRelays)
	}
	multiplier := float64(numEstimatedCU) / float64(numClaimedCU)
	estimatedRelays := int64(float64(numRelays) * multiplier)
	return multiplier, estimatedRelays
}

// convertSettledEvent transforms an EventClaimSettled proto into a store.Settlement.
// Uses computeDifficulty for estimated relays and parseCoinAmount for ClaimedUpokt.
// Overservice correlation is NOT done here (see correlateOverservice).
func convertSettledEvent(event *tokenomicstypes.EventClaimSettled, height int64, ts time.Time) store.Settlement {
	multiplier, estimatedRelays := computeDifficulty(
		event.NumRelays, event.NumClaimedComputeUnits, event.NumEstimatedComputeUnits,
	)
	return store.Settlement{
		BlockHeight:              height,
		BlockTimestamp:           ts,
		EventType:                "settled",
		SupplierOperatorAddress:  event.SupplierOperatorAddress,
		ApplicationAddress:       event.ApplicationAddress,
		ServiceID:                event.ServiceId,
		SessionEndBlockHeight:    event.SessionEndBlockHeight,
		ClaimProofStatus:         event.ClaimProofStatusInt,
		ClaimedUpokt:             parseCoinAmount(event.ClaimedUpokt),
		NumRelays:                int64(event.NumRelays),
		NumClaimedComputeUnits:   int64(event.NumClaimedComputeUnits),
		NumEstimatedComputeUnits: int64(event.NumEstimatedComputeUnits),
		ProofRequirement:         event.ProofRequirementInt,
		EstimatedRelays:          estimatedRelays,
		DifficultyMultiplier:     multiplier,
	}
}

// convertSettledEventWithRewards transforms an EventClaimSettled proto into a
// store.Settlement and its associated RewardDistributions.
// The RewardDistribution map[string]string is parsed into []store.RewardDistribution.
func convertSettledEventWithRewards(event *tokenomicstypes.EventClaimSettled, height int64, ts time.Time) (store.Settlement, []store.RewardDistribution) {
	settlement := convertSettledEvent(event, height, ts)

	var rewards []store.RewardDistribution
	for addr, amountStr := range event.RewardDistribution {
		rewards = append(rewards, store.RewardDistribution{
			Address:     addr,
			AmountUpokt: parseCoinAmount(amountStr),
		})
	}

	return settlement, rewards
}

// convertExpiredEvent transforms an EventClaimExpired proto into a store.Settlement.
// Uses computeDifficulty for estimated relays. ExpirationReason is stored as the enum string name.
func convertExpiredEvent(event *tokenomicstypes.EventClaimExpired, height int64, ts time.Time) store.Settlement {
	multiplier, estimatedRelays := computeDifficulty(
		event.NumRelays, event.NumClaimedComputeUnits, event.NumEstimatedComputeUnits,
	)
	return store.Settlement{
		BlockHeight:              height,
		BlockTimestamp:           ts,
		EventType:                "expired",
		SupplierOperatorAddress:  event.SupplierOperatorAddress,
		ApplicationAddress:       event.ApplicationAddress,
		ServiceID:                event.ServiceId,
		SessionEndBlockHeight:    event.SessionEndBlockHeight,
		ClaimProofStatus:         event.ClaimProofStatusInt,
		ClaimedUpokt:             parseCoinAmount(event.ClaimedUpokt),
		NumRelays:                int64(event.NumRelays),
		NumClaimedComputeUnits:   int64(event.NumClaimedComputeUnits),
		NumEstimatedComputeUnits: int64(event.NumEstimatedComputeUnits),
		ExpirationReason:         event.ExpirationReason.String(),
		EstimatedRelays:          estimatedRelays,
		DifficultyMultiplier:     multiplier,
	}
}

// convertSlashedEvent transforms an EventSupplierSlashed proto into a store.Settlement.
// Uses parseCoinAmount for ProofMissingPenalty coin string.
func convertSlashedEvent(event *tokenomicstypes.EventSupplierSlashed, height int64, ts time.Time) store.Settlement {
	return store.Settlement{
		BlockHeight:             height,
		BlockTimestamp:          ts,
		EventType:               "slashed",
		SupplierOperatorAddress: event.SupplierOperatorAddress,
		ApplicationAddress:      event.ApplicationAddress,
		ServiceID:               event.ServiceId,
		SessionEndBlockHeight:   event.SessionEndBlockHeight,
		ClaimProofStatus:        event.ClaimProofStatusInt,
		SlashPenaltyUpokt:       parseCoinAmount(event.ProofMissingPenalty),
	}
}

// convertDiscardedEvent transforms an EventClaimDiscarded proto into a store.Settlement.
func convertDiscardedEvent(event *tokenomicstypes.EventClaimDiscarded, height int64, ts time.Time) store.Settlement {
	return store.Settlement{
		BlockHeight:             height,
		BlockTimestamp:          ts,
		EventType:               "discarded",
		SupplierOperatorAddress: event.SupplierOperatorAddress,
		ApplicationAddress:      event.ApplicationAddress,
		ServiceID:               event.ServiceId,
		SessionEndBlockHeight:   event.SessionEndBlockHeight,
		ClaimProofStatus:        event.ClaimProofStatusInt,
		ErrorMessage:            event.Error,
	}
}

// convertOverserviceEvent transforms an EventApplicationOverserviced proto into a store.OverserviceEvent.
// Note: uses event.SupplierOperatorAddr (abbreviated form), not SupplierOperatorAddress.
func convertOverserviceEvent(event *tokenomicstypes.EventApplicationOverserviced, height int64, ts time.Time) store.OverserviceEvent {
	return store.OverserviceEvent{
		BlockHeight:             height,
		BlockTimestamp:          ts,
		ApplicationAddress:      event.ApplicationAddr,
		SupplierOperatorAddress: event.SupplierOperatorAddr,
		ExpectedBurnUpokt:       parseCoinAmount(event.ExpectedBurn),
		EffectiveBurnUpokt:      parseCoinAmount(event.EffectiveBurn),
	}
}

// convertReimbursementEvent transforms an EventApplicationReimbursementRequest proto
// into a store.ReimbursementEvent.
// Note: uses event.SupplierOperatorAddr, event.SupplierOwnerAddr, event.ApplicationAddr
// (abbreviated forms).
func convertReimbursementEvent(event *tokenomicstypes.EventApplicationReimbursementRequest, height int64, ts time.Time) store.ReimbursementEvent {
	return store.ReimbursementEvent{
		BlockHeight:             height,
		BlockTimestamp:          ts,
		ApplicationAddress:      event.ApplicationAddr,
		SupplierOperatorAddress: event.SupplierOperatorAddr,
		SupplierOwnerAddress:    event.SupplierOwnerAddr,
		ServiceID:               event.ServiceId,
		SessionID:               event.SessionId,
		AmountUpokt:             parseCoinAmount(event.Amount),
	}
}

// correlateOverservice implements the two-pass in-memory overservice correlation algorithm.
// Pass 1: Build overserviceMap from EventApplicationOverserviced events.
//
//	Note: uses SupplierOperatorAddr and ApplicationAddr (abbreviated field names).
//
// Pass 2: Iterate settlements and mark matching "settled" entries with overservice data.
// Returns the number of correlations made.
func correlateOverservice(events []subscriber.SettlementEvent, settlements []store.Settlement) int {
	// Pass 1: Build overservice map from overservice events
	overserviceMap := make(map[overserviceKey]overserviceData)
	for _, se := range events {
		overserviced, ok := se.Event.(*tokenomicstypes.EventApplicationOverserviced)
		if !ok {
			continue
		}
		key := overserviceKey{
			SupplierOperatorAddress: overserviced.SupplierOperatorAddr,
			ApplicationAddress:      overserviced.ApplicationAddr,
		}
		overserviceMap[key] = overserviceData{
			ExpectedBurnUpokt:  parseCoinAmount(overserviced.ExpectedBurn),
			EffectiveBurnUpokt: parseCoinAmount(overserviced.EffectiveBurn),
		}
	}

	if len(overserviceMap) == 0 {
		return 0
	}

	// Pass 2: Correlate with settled claims only
	correlationCount := 0
	for i := range settlements {
		if settlements[i].EventType != "settled" {
			continue
		}
		key := overserviceKey{
			SupplierOperatorAddress: settlements[i].SupplierOperatorAddress,
			ApplicationAddress:      settlements[i].ApplicationAddress,
		}
		if osData, found := overserviceMap[key]; found {
			settlements[i].IsOverserviced = true
			settlements[i].EffectiveBurnUpokt = osData.EffectiveBurnUpokt
			settlements[i].OverserviceDiffUpokt = osData.ExpectedBurnUpokt - osData.EffectiveBurnUpokt
			correlationCount++
		}
	}

	return correlationCount
}
