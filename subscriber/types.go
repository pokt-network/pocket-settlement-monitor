package subscriber

import (
	"fmt"
	"sync"
	"time"

	"github.com/cosmos/gogoproto/proto"
)

// SettlementEvent wraps a decoded proto message with block context.
// The subscriber emits raw proto messages; the processor (Phase 4) converts to store types.
type SettlementEvent struct {
	Height    int64
	EventType string        // e.g. "pokt.tokenomics.EventClaimSettled"
	Event     proto.Message // gogoproto Message, NOT google protobuf
}

// BlockEvents holds all settlement events decoded from a single block.
// Delivered as a batch on the output channel (NOT individual events).
// Critical for mainnet where blocks can contain 200k+ settlement events.
type BlockEvents struct {
	Height    int64
	Timestamp time.Time
	Events    []SettlementEvent
}

// ConnectionState represents the subscriber's connection state.
type ConnectionState int

const (
	StateConnected ConnectionState = iota
	StateDisconnected
	StateReconnected
)

// String returns a human-readable representation of the connection state.
func (s ConnectionState) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	case StateReconnected:
		return "reconnected"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// StateChangeEvent is emitted when the subscriber's connection state changes.
// Phase 4 metrics layer hooks into this for the websocket_connected gauge.
type StateChangeEvent struct {
	State          ConnectionState
	LastSeenHeight int64 // set on disconnect/reconnect
	ResumeHeight   int64 // set on reconnect (first block after reconnection)
}

// StateChangeCallback is called when connection state changes.
type StateChangeCallback func(StateChangeEvent)

// BlockCallback is called on every block received from the WebSocket,
// regardless of whether it contains settlement events. This allows the
// monitor to track block height and processing rate across all blocks.
type BlockCallback func(height int64)

// GapCallback is called when the subscriber detects a height gap after
// reconnecting (e.g., lastSeenHeight=100, new block=103 → gap 101-102).
// The monitor wires this to trigger backfill of the missed blocks.
type GapCallback func(fromHeight, toHeight int64)

// DecodeStats tracks per-event-type decode failure counts.
// Thread-safe via mutex. Feeds into a Prometheus counter in Phase 4.
type DecodeStats struct {
	mu       sync.Mutex
	Failures map[string]int64
}

// NewDecodeStats creates a new DecodeStats instance.
func NewDecodeStats() *DecodeStats {
	return &DecodeStats{
		Failures: make(map[string]int64),
	}
}

// RecordFailure increments the failure count for the given event type.
func (d *DecodeStats) RecordFailure(eventType string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Failures[eventType]++
}

// GetFailures returns a copy of the failure counts map.
func (d *DecodeStats) GetFailures() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make(map[string]int64, len(d.Failures))
	for k, v := range d.Failures {
		result[k] = v
	}
	return result
}
