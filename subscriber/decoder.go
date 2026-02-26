package subscriber

import (
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	comettypes "github.com/cometbft/cometbft/types"
	cosmostypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"
	"github.com/rs/zerolog"
)

// targetEventTypes contains all 6 recognized tokenomics event type strings.
// Built using proto.MessageName to stay in sync with proto definitions.
// The import of tokenomicstypes triggers init() registration of all proto types,
// which is required for ParseTypedEvent to work.
var targetEventTypes = map[string]bool{
	proto.MessageName(&tokenomicstypes.EventClaimSettled{}):                    true,
	proto.MessageName(&tokenomicstypes.EventClaimExpired{}):                    true,
	proto.MessageName(&tokenomicstypes.EventSupplierSlashed{}):                 true,
	proto.MessageName(&tokenomicstypes.EventClaimDiscarded{}):                  true,
	proto.MessageName(&tokenomicstypes.EventApplicationOverserviced{}):         true,
	proto.MessageName(&tokenomicstypes.EventApplicationReimbursementRequest{}): true,
}

// standardCosmosEvents are event types emitted by every block as part of
// normal Cosmos SDK / CometBFT operation. These are silently ignored.
var standardCosmosEvents = map[string]bool{
	"coin_spent":    true,
	"coin_received": true,
	"coinbase":      true,
	"transfer":      true,
	"message":       true,
	"mint":          true,
	"commission":    true,
	"rewards":       true,
	"burn":          true,
}

// knownNonProtoAttrs contains attribute keys injected by the Cosmos SDK event
// manager that are not part of the proto message. These break ParseTypedEvent's
// JSON reconstruction because their values are plain strings (not JSON-encoded).
// Example: "mode" has value "EndBlock" which fails json.RawMessage parsing.
var knownNonProtoAttrs = map[string]bool{
	"mode": true,
}

// filterEventAttrs returns a copy of the ABCI event with non-proto attributes removed.
// This is required before calling ParseTypedEvent because Cosmos SDK injects
// attributes like "mode" with plain string values that break JSON parsing.
func filterEventAttrs(event abci.Event) abci.Event {
	filtered := make([]abci.EventAttribute, 0, len(event.Attributes))
	for _, attr := range event.Attributes {
		if knownNonProtoAttrs[attr.Key] {
			continue
		}
		filtered = append(filtered, attr)
	}
	return abci.Event{
		Type:       event.Type,
		Attributes: filtered,
	}
}

// decodeEvent attempts to decode an ABCI event into a SettlementEvent.
// Returns the decoded event and true on success, or an empty event and false if:
// - The event type is not in targetEventTypes (silently skipped)
// - ParseTypedEvent fails for a known event type (logged at ERROR level, failure tracked in stats)
func decodeEvent(event abci.Event, height int64, logger zerolog.Logger, stats *DecodeStats) (SettlementEvent, bool) {
	if !targetEventTypes[event.Type] {
		return SettlementEvent{}, false
	}

	// Strip non-proto attributes (e.g. "mode") that would break JSON reconstruction.
	cleaned := filterEventAttrs(event)

	msg, err := cosmostypes.ParseTypedEvent(cleaned)
	if err != nil {
		// Known event type failed to parse: log ERROR (higher severity per locked decision)
		logger.Error().
			Err(err).
			Str("event_type", event.Type).
			Int64("height", height).
			Msg("ParseTypedEvent failed for known event type")

		stats.RecordFailure(event.Type)
		return SettlementEvent{}, false
	}

	return SettlementEvent{
		Height:    height,
		EventType: event.Type,
		Event:     msg,
	}, true
}

// decodeBlockEventsCommon is the shared decoding logic for both live and backfill paths.
// It decodes settlement events, logs a per-block summary, and warns about
// unexpected (non-standard, non-target) event types seen for the first time.
func decodeBlockEventsCommon(abciEvents []abci.Event, height int64, blockTime time.Time, logger zerolog.Logger, stats *DecodeStats) BlockEvents {
	var events []SettlementEvent
	var skipped int
	unexpected := make(map[string]int)

	for _, abciEvent := range abciEvents {
		if targetEventTypes[abciEvent.Type] {
			se, ok := decodeEvent(abciEvent, height, logger, stats)
			if ok {
				events = append(events, se)
			}
			continue
		}

		skipped++
		if !standardCosmosEvents[abciEvent.Type] {
			unexpected[abciEvent.Type]++
		}
	}

	// Log unexpected (non-standard) event types at debug — these might be
	// new module events worth investigating.
	for eventType, count := range unexpected {
		logger.Debug().
			Str("event_type", eventType).
			Int("count", count).
			Int64("height", height).
			Msg("non-standard event type seen (not a settlement type)")
	}

	return BlockEvents{Height: height, Timestamp: blockTime, Events: events}
}

// decodeBlockEvents decodes all settlement events from a single NewBlockEvents message.
// Returns a BlockEvents containing only successfully decoded settlement events.
// Non-settlement events are silently skipped. Decode failures for known event types
// are logged and tracked in stats.
func decodeBlockEvents(data comettypes.EventDataNewBlockEvents, blockTime time.Time, logger zerolog.Logger, stats *DecodeStats) BlockEvents {
	return decodeBlockEventsCommon(data.Events, data.Height, blockTime, logger, stats)
}

// DecodeBlockResults decodes settlement events from a BlockResults response.
// Used by the backfill path to reuse the same decoding logic as the live WebSocket path.
// The events come from FinalizeBlockEvents (same ABCI events as NewBlockEvents).
// Accepts []abci.Event rather than the full ResultBlockResults to keep the subscriber
// package independent of RPC types. The caller extracts FinalizeBlockEvents and passes it in.
func DecodeBlockResults(abciEvents []abci.Event, height int64, blockTime time.Time, logger zerolog.Logger, stats *DecodeStats) BlockEvents {
	return decodeBlockEventsCommon(abciEvents, height, blockTime, logger, stats)
}

// targetEventTypeCount returns the number of recognized event types.
// Useful for tests to verify all 6 types are registered.
func targetEventTypeCount() int {
	return len(targetEventTypes)
}
