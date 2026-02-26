package subscriber

import (
	"bytes"
	"io"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	comettypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/proto"
	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestEvent creates an ABCI event with the given type and attributes.
// This helper reduces test boilerplate for constructing ABCI events.
func makeTestEvent(eventType string, attrs map[string]string) abci.Event {
	abciAttrs := make([]abci.EventAttribute, 0, len(attrs))
	for k, v := range attrs {
		abciAttrs = append(abciAttrs, abci.EventAttribute{
			Key:   k,
			Value: v,
		})
	}
	return abci.Event{
		Type:       eventType,
		Attributes: abciAttrs,
	}
}

// makeTestEventOrdered creates an ABCI event with attributes in the order provided.
// Use this when attribute ordering matters for test determinism.
func makeTestEventOrdered(eventType string, keys []string, values []string) abci.Event {
	abciAttrs := make([]abci.EventAttribute, 0, len(keys))
	for i := range keys {
		abciAttrs = append(abciAttrs, abci.EventAttribute{
			Key:   keys[i],
			Value: values[i],
		})
	}
	return abci.Event{
		Type:       eventType,
		Attributes: abciAttrs,
	}
}

func TestFilterEventAttrs(t *testing.T) {
	t.Run("strips mode attribute", func(t *testing.T) {
		event := abci.Event{
			Type: "pocket.tokenomics.EventClaimSettled",
			Attributes: []abci.EventAttribute{
				{Key: "supplier_operator_address", Value: `"pokt1supplier"`},
				{Key: "mode", Value: "EndBlock"},
				{Key: "service_id", Value: `"svc01"`},
			},
		}

		filtered := filterEventAttrs(event)

		assert.Equal(t, "pocket.tokenomics.EventClaimSettled", filtered.Type)
		assert.Len(t, filtered.Attributes, 2)
		for _, attr := range filtered.Attributes {
			assert.NotEqual(t, "mode", attr.Key, "mode attribute should have been stripped")
		}
	})

	t.Run("preserves non-mode attributes", func(t *testing.T) {
		event := abci.Event{
			Type: "pocket.tokenomics.EventClaimSettled",
			Attributes: []abci.EventAttribute{
				{Key: "supplier_operator_address", Value: `"pokt1supplier"`},
				{Key: "service_id", Value: `"svc01"`},
			},
		}

		filtered := filterEventAttrs(event)

		assert.Len(t, filtered.Attributes, 2)
		assert.Equal(t, "supplier_operator_address", filtered.Attributes[0].Key)
		assert.Equal(t, "service_id", filtered.Attributes[1].Key)
	})

	t.Run("handles empty attributes", func(t *testing.T) {
		event := abci.Event{
			Type:       "pocket.tokenomics.EventClaimSettled",
			Attributes: nil,
		}

		filtered := filterEventAttrs(event)

		assert.Equal(t, "pocket.tokenomics.EventClaimSettled", filtered.Type)
		assert.Empty(t, filtered.Attributes)
	})

	t.Run("handles event with only mode attribute", func(t *testing.T) {
		event := abci.Event{
			Type: "some.event",
			Attributes: []abci.EventAttribute{
				{Key: "mode", Value: "EndBlock"},
			},
		}

		filtered := filterEventAttrs(event)

		assert.Equal(t, "some.event", filtered.Type)
		assert.Empty(t, filtered.Attributes)
	})
}

func TestDecodeEvent_AllSixTypes(t *testing.T) {
	logger := zerolog.New(io.Discard)
	stats := NewDecodeStats()
	height := int64(1000)

	// Each test case uses the proto.MessageName to get the correct event type string,
	// and provides the minimum set of attributes needed for successful parsing.
	// All attribute values must be valid JSON since ParseTypedEvent uses json.RawMessage.
	testCases := []struct {
		name      string
		protoMsg  proto.Message
		attrs     []string // alternating key, value pairs
		checkFunc func(t *testing.T, se SettlementEvent)
	}{
		{
			name:     "EventClaimSettled",
			protoMsg: &tokenomicstypes.EventClaimSettled{},
			attrs: []string{
				"supplier_operator_address", `"pokt1supplier"`,
				"application_address", `"pokt1app"`,
				"service_id", `"svc01"`,
				"num_relays", `"50"`,
				"num_claimed_compute_units", `"100"`,
				"num_estimated_compute_units", `"200"`,
				"claimed_upokt", `"1000upokt"`,
				"proof_requirement_int", `1`,
				"claim_proof_status_int", `2`,
				"session_end_block_height", `"900"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventClaimSettled)
				require.True(t, ok, "expected *EventClaimSettled, got %T", se.Event)
				assert.Equal(t, "pokt1supplier", msg.GetSupplierOperatorAddress())
				assert.Equal(t, "pokt1app", msg.GetApplicationAddress())
				assert.Equal(t, "svc01", msg.GetServiceId())
				assert.Equal(t, uint64(50), msg.GetNumRelays())
				assert.Equal(t, uint64(100), msg.GetNumClaimedComputeUnits())
				assert.Equal(t, uint64(200), msg.GetNumEstimatedComputeUnits())
				assert.Equal(t, "1000upokt", msg.GetClaimedUpokt())
			},
		},
		{
			name:     "EventClaimExpired",
			protoMsg: &tokenomicstypes.EventClaimExpired{},
			attrs: []string{
				"supplier_operator_address", `"pokt1supplier"`,
				"application_address", `"pokt1app"`,
				"service_id", `"svc02"`,
				"num_relays", `"30"`,
				"num_claimed_compute_units", `"60"`,
				"num_estimated_compute_units", `"120"`,
				"claimed_upokt", `"500upokt"`,
				"expiration_reason", `1`,
				"claim_proof_status_int", `0`,
				"session_end_block_height", `"800"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventClaimExpired)
				require.True(t, ok, "expected *EventClaimExpired, got %T", se.Event)
				assert.Equal(t, "pokt1supplier", msg.GetSupplierOperatorAddress())
				assert.Equal(t, "svc02", msg.GetServiceId())
				assert.Equal(t, uint64(30), msg.GetNumRelays())
			},
		},
		{
			name:     "EventSupplierSlashed",
			protoMsg: &tokenomicstypes.EventSupplierSlashed{},
			attrs: []string{
				"supplier_operator_address", `"pokt1slashed"`,
				"application_address", `"pokt1app"`,
				"service_id", `"svc03"`,
				"proof_missing_penalty", `"200upokt"`,
				"claim_proof_status_int", `2`,
				"session_end_block_height", `"700"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventSupplierSlashed)
				require.True(t, ok, "expected *EventSupplierSlashed, got %T", se.Event)
				assert.Equal(t, "pokt1slashed", msg.GetSupplierOperatorAddress())
				assert.Equal(t, "200upokt", msg.GetProofMissingPenalty())
			},
		},
		{
			name:     "EventClaimDiscarded",
			protoMsg: &tokenomicstypes.EventClaimDiscarded{},
			attrs: []string{
				"supplier_operator_address", `"pokt1discarded"`,
				"application_address", `"pokt1app"`,
				"service_id", `"svc04"`,
				"error", `"some error occurred"`,
				"claim_proof_status_int", `0`,
				"session_end_block_height", `"600"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventClaimDiscarded)
				require.True(t, ok, "expected *EventClaimDiscarded, got %T", se.Event)
				assert.Equal(t, "pokt1discarded", msg.GetSupplierOperatorAddress())
				assert.Equal(t, "some error occurred", msg.GetError())
			},
		},
		{
			name:     "EventApplicationOverserviced",
			protoMsg: &tokenomicstypes.EventApplicationOverserviced{},
			attrs: []string{
				"application_addr", `"pokt1app"`,
				"supplier_operator_addr", `"pokt1supplier"`,
				"expected_burn", `"1000upokt"`,
				"effective_burn", `"500upokt"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventApplicationOverserviced)
				require.True(t, ok, "expected *EventApplicationOverserviced, got %T", se.Event)
				assert.Equal(t, "pokt1app", msg.GetApplicationAddr())
				assert.Equal(t, "pokt1supplier", msg.GetSupplierOperatorAddr())
				assert.Equal(t, "1000upokt", msg.GetExpectedBurn())
				assert.Equal(t, "500upokt", msg.GetEffectiveBurn())
			},
		},
		{
			name:     "EventApplicationReimbursementRequest",
			protoMsg: &tokenomicstypes.EventApplicationReimbursementRequest{},
			attrs: []string{
				"application_addr", `"pokt1app"`,
				"supplier_operator_addr", `"pokt1supplier"`,
				"supplier_owner_addr", `"pokt1owner"`,
				"service_id", `"svc06"`,
				"session_id", `"session123"`,
				"amount", `"750upokt"`,
				"mode", "EndBlock",
			},
			checkFunc: func(t *testing.T, se SettlementEvent) {
				msg, ok := se.Event.(*tokenomicstypes.EventApplicationReimbursementRequest)
				require.True(t, ok, "expected *EventApplicationReimbursementRequest, got %T", se.Event)
				assert.Equal(t, "pokt1app", msg.GetApplicationAddr())
				assert.Equal(t, "pokt1supplier", msg.GetSupplierOperatorAddr())
				assert.Equal(t, "pokt1owner", msg.GetSupplierOwnerAddr())
				assert.Equal(t, "svc06", msg.GetServiceId())
				assert.Equal(t, "session123", msg.GetSessionId())
				assert.Equal(t, "750upokt", msg.GetAmount())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			eventType := proto.MessageName(tc.protoMsg)
			require.NotEmpty(t, eventType, "proto.MessageName returned empty for %T", tc.protoMsg)

			// Build ABCI event with ordered attributes
			keys := make([]string, 0, len(tc.attrs)/2)
			values := make([]string, 0, len(tc.attrs)/2)
			for i := 0; i < len(tc.attrs); i += 2 {
				keys = append(keys, tc.attrs[i])
				values = append(values, tc.attrs[i+1])
			}
			event := makeTestEventOrdered(eventType, keys, values)

			se, ok := decodeEvent(event, height, logger, stats)
			require.True(t, ok, "decodeEvent failed for %s", tc.name)
			assert.Equal(t, height, se.Height)
			assert.Equal(t, eventType, se.EventType)
			assert.NotNil(t, se.Event)

			tc.checkFunc(t, se)
		})
	}

	// Verify no decode failures occurred
	failures := stats.GetFailures()
	assert.Empty(t, failures, "expected no decode failures, got: %v", failures)
}

func TestDecodeEvent_UnknownType(t *testing.T) {
	// Unknown event types are silently skipped at the event level.
	// Logging of non-standard types happens at the block level in decodeBlockEventsCommon.
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)
	stats := NewDecodeStats()

	event := makeTestEvent("unknown.event.Type", map[string]string{
		"some_key": `"some_value"`,
	})

	se, ok := decodeEvent(event, 500, logger, stats)

	assert.False(t, ok, "unknown event type should return false")
	assert.Equal(t, SettlementEvent{}, se)

	// decodeEvent no longer logs unknown types (too noisy per-event).
	// Verify no log output and no error-level entries.
	logOutput := logBuf.String()
	assert.Empty(t, logOutput, "decodeEvent should not log for unknown event types")

	// Verify no failures tracked (unknown types are not failures)
	failures := stats.GetFailures()
	assert.Empty(t, failures, "unknown event types should not be tracked as failures")
}

func TestDecodeEvent_ParseError(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.ErrorLevel)
	stats := NewDecodeStats()

	// Use a known event type but with malformed attribute values
	eventType := proto.MessageName(&tokenomicstypes.EventClaimSettled{})
	require.NotEmpty(t, eventType)

	event := makeTestEvent(eventType, map[string]string{
		"supplier_operator_address": "not_valid_json{{{",
		"mode":                      "EndBlock", // should be filtered
	})

	se, ok := decodeEvent(event, 600, logger, stats)

	assert.False(t, ok, "parse error should return false")
	assert.Equal(t, SettlementEvent{}, se)

	// Verify ERROR log was emitted
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "ParseTypedEvent failed for known event type")
	assert.Contains(t, logOutput, `"level":"error"`)

	// Verify failure was tracked for the specific event type
	failures := stats.GetFailures()
	assert.Equal(t, int64(1), failures[eventType],
		"expected 1 failure for %s, got %d", eventType, failures[eventType])
}

func TestDecodeBlockEvents_Batch(t *testing.T) {
	logger := zerolog.New(io.Discard)
	stats := NewDecodeStats()
	height := int64(2000)

	settledType := proto.MessageName(&tokenomicstypes.EventClaimSettled{})
	expiredType := proto.MessageName(&tokenomicstypes.EventClaimExpired{})

	data := comettypes.EventDataNewBlockEvents{
		Height: height,
		Events: []abci.Event{
			// Event 1: valid EventClaimSettled
			makeTestEventOrdered(settledType, []string{
				"supplier_operator_address",
				"application_address",
				"service_id",
				"num_relays",
				"num_claimed_compute_units",
				"num_estimated_compute_units",
				"claimed_upokt",
				"proof_requirement_int",
				"claim_proof_status_int",
				"session_end_block_height",
				"mode",
			}, []string{
				`"pokt1supplier1"`,
				`"pokt1app"`,
				`"svc01"`,
				`"10"`,
				`"20"`,
				`"40"`,
				`"100upokt"`,
				`0`,
				`1`,
				`"900"`,
				"EndBlock",
			}),
			// Event 2: unknown event type (should be skipped)
			makeTestEvent("cosmos.bank.v1beta1.EventTransfer", map[string]string{
				"sender":   `"pokt1sender"`,
				"receiver": `"pokt1receiver"`,
			}),
			// Event 3: valid EventClaimExpired
			makeTestEventOrdered(expiredType, []string{
				"supplier_operator_address",
				"application_address",
				"service_id",
				"num_relays",
				"num_claimed_compute_units",
				"num_estimated_compute_units",
				"claimed_upokt",
				"expiration_reason",
				"claim_proof_status_int",
				"session_end_block_height",
				"mode",
			}, []string{
				`"pokt1supplier2"`,
				`"pokt1app2"`,
				`"svc02"`,
				`"5"`,
				`"10"`,
				`"20"`,
				`"50upokt"`,
				`1`,
				`0`,
				`"800"`,
				"EndBlock",
			}),
			// Event 4: another unknown event type
			makeTestEvent("tendermint.abci.Event", map[string]string{
				"key": `"value"`,
			}),
		},
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	result := decodeBlockEvents(data, testTime, logger, stats)

	assert.Equal(t, height, result.Height)
	assert.Equal(t, testTime, result.Timestamp)
	require.Len(t, result.Events, 2, "expected 2 settlement events from 4 total events")

	// Verify the two decoded events are the correct types
	assert.Equal(t, settledType, result.Events[0].EventType)
	assert.Equal(t, expiredType, result.Events[1].EventType)

	// Verify both events have correct height
	for _, ev := range result.Events {
		assert.Equal(t, height, ev.Height)
	}

	// Verify no decode failures
	failures := stats.GetFailures()
	assert.Empty(t, failures, "expected no decode failures for valid events")
}

func TestDecodeBlockEvents_EmptyBlock(t *testing.T) {
	logger := zerolog.New(io.Discard)
	stats := NewDecodeStats()

	data := comettypes.EventDataNewBlockEvents{
		Height: 3000,
		Events: nil,
	}

	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	result := decodeBlockEvents(data, testTime, logger, stats)

	assert.Equal(t, int64(3000), result.Height)
	assert.Equal(t, testTime, result.Timestamp)
	assert.Empty(t, result.Events)
}

func TestTargetEventTypeCount(t *testing.T) {
	assert.Equal(t, 6, targetEventTypeCount(), "expected exactly 6 target event types")
}

func TestDecodeStats_ThreadSafety(t *testing.T) {
	stats := NewDecodeStats()

	// Simulate concurrent access
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			stats.RecordFailure("type.A")
			stats.RecordFailure("type.B")
			_ = stats.GetFailures()
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	failures := stats.GetFailures()
	assert.Equal(t, int64(10), failures["type.A"])
	assert.Equal(t, int64(10), failures["type.B"])
}

func TestDecodeStats_GetFailures_ReturnsCopy(t *testing.T) {
	stats := NewDecodeStats()
	stats.RecordFailure("type.A")

	// Get copy and modify it
	copy1 := stats.GetFailures()
	copy1["type.A"] = 999

	// Original should be unaffected
	copy2 := stats.GetFailures()
	assert.Equal(t, int64(1), copy2["type.A"],
		"modifying returned map should not affect internal state")
}
