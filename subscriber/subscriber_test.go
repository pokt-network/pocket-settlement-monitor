package subscriber

import (
	"context"
	"sync"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	comettypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/proto"
	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEventSource is a test EventSource that returns controlled channels.
type mockEventSource struct {
	mu          sync.Mutex
	closed      bool
	closeCh     chan struct{}
	subscribeFn func(ctx context.Context) (<-chan coretypes.ResultEvent, error)
}

func newMockEventSource(fn func(ctx context.Context) (<-chan coretypes.ResultEvent, error)) *mockEventSource {
	return &mockEventSource{
		closeCh:     make(chan struct{}),
		subscribeFn: fn,
	}
}

func (m *mockEventSource) Subscribe(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
	return m.subscribeFn(ctx)
}

func (m *mockEventSource) Header(_ context.Context, height int64) (time.Time, error) {
	// Return a deterministic timestamp for testing.
	return time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC).Add(time.Duration(height) * time.Minute), nil
}

func (m *mockEventSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.closeCh)
	}
	return nil
}

// makeResultEvent creates a coretypes.ResultEvent wrapping EventDataNewBlockEvents.
func makeResultEvent(height int64, events []abci.Event) coretypes.ResultEvent {
	return coretypes.ResultEvent{
		Data: comettypes.EventDataNewBlockEvents{
			Height: height,
			Events: events,
		},
	}
}

// makeSettledABCIEvent creates a valid EventClaimSettled ABCI event for testing.
func makeSettledABCIEvent(supplier, app, service string) abci.Event {
	eventType := proto.MessageName(&tokenomicstypes.EventClaimSettled{})
	return makeTestEventOrdered(eventType, []string{
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
		`"` + supplier + `"`,
		`"` + app + `"`,
		`"` + service + `"`,
		`"50"`,
		`"100"`,
		`"200"`,
		`"1000upokt"`,
		`1`,
		`2`,
		`"900"`,
		"EndBlock",
	})
}

// makeExpiredABCIEvent creates a valid EventClaimExpired ABCI event for testing.
func makeExpiredABCIEvent(supplier, app, service string) abci.Event {
	eventType := proto.MessageName(&tokenomicstypes.EventClaimExpired{})
	return makeTestEventOrdered(eventType, []string{
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
		`"` + supplier + `"`,
		`"` + app + `"`,
		`"` + service + `"`,
		`"30"`,
		`"60"`,
		`"120"`,
		`"500upokt"`,
		`1`,
		`0`,
		`"800"`,
		"EndBlock",
	})
}

func TestSubscriber_EmitsBlockEvents(t *testing.T) {
	ch := make(chan coretypes.ResultEvent, 10)

	source := newMockEventSource(func(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
		return ch, nil
	})

	logger := zerolog.Nop()
	sub := NewSubscriber(source, logger, 10*time.Millisecond, 50*time.Millisecond, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(ctx)
	}()

	// Send a ResultEvent with 2 valid settlement events and 1 unknown
	ch <- makeResultEvent(100, []abci.Event{
		makeSettledABCIEvent("pokt1supplier", "pokt1app", "svc01"),
		makeExpiredABCIEvent("pokt1supplier2", "pokt1app2", "svc02"),
		makeTestEvent("unknown.event.Type", map[string]string{"key": `"value"`}),
	})

	// Read from subscriber's output channel
	select {
	case blockEvents := <-sub.Events():
		assert.Equal(t, int64(100), blockEvents.Height)
		require.Len(t, blockEvents.Events, 2, "expected 2 settlement events (unknown filtered out)")
		assert.Equal(t, proto.MessageName(&tokenomicstypes.EventClaimSettled{}), blockEvents.Events[0].EventType)
		assert.Equal(t, proto.MessageName(&tokenomicstypes.EventClaimExpired{}), blockEvents.Events[1].EventType)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for block events")
	}

	cancel()
	select {
	case err := <-runDone:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestSubscriber_ReconnectsOnSourceClose(t *testing.T) {
	ch1 := make(chan coretypes.ResultEvent, 10)
	ch2 := make(chan coretypes.ResultEvent, 10)

	callCount := 0
	var callMu sync.Mutex

	source := newMockEventSource(func(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
		callMu.Lock()
		defer callMu.Unlock()
		callCount++
		if callCount == 1 {
			return ch1, nil
		}
		return ch2, nil
	})

	var stateEvents []StateChangeEvent
	var stateMu sync.Mutex
	stateCallback := func(ev StateChangeEvent) {
		stateMu.Lock()
		defer stateMu.Unlock()
		stateEvents = append(stateEvents, ev)
	}

	logger := zerolog.Nop()
	sub := NewSubscriber(source, logger, 10*time.Millisecond, 50*time.Millisecond, 0, stateCallback)
	// Use short heartbeat for testing
	sub.heartbeatTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(ctx)
	}()

	// Send one event on first channel, verify received
	ch1 <- makeResultEvent(100, []abci.Event{
		makeSettledABCIEvent("pokt1s1", "pokt1a1", "svc01"),
	})

	select {
	case blockEvents := <-sub.Events():
		assert.Equal(t, int64(100), blockEvents.Height)
		require.Len(t, blockEvents.Events, 1)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first block event")
	}

	// Close the first channel to simulate disconnect
	close(ch1)

	// Wait for reconnection to succeed (should happen quickly with 10ms base delay)
	time.Sleep(500 * time.Millisecond)

	// Send event on second channel, verify received
	ch2 <- makeResultEvent(105, []abci.Event{
		makeSettledABCIEvent("pokt1s2", "pokt1a2", "svc02"),
	})

	select {
	case blockEvents := <-sub.Events():
		assert.Equal(t, int64(105), blockEvents.Height)
		require.Len(t, blockEvents.Events, 1)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second block event after reconnection")
	}

	// Verify state change callbacks fired
	stateMu.Lock()
	events := make([]StateChangeEvent, len(stateEvents))
	copy(events, stateEvents)
	stateMu.Unlock()

	require.GreaterOrEqual(t, len(events), 3, "expected at least Connected, Disconnected, Reconnected events")
	assert.Equal(t, StateConnected, events[0].State)
	assert.Equal(t, StateDisconnected, events[1].State)
	assert.Equal(t, int64(100), events[1].LastSeenHeight)
	assert.Equal(t, StateReconnected, events[2].State)
	assert.Equal(t, int64(100), events[2].LastSeenHeight)

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestSubscriber_StateChangeCallbacks(t *testing.T) {
	ch1 := make(chan coretypes.ResultEvent, 10)
	ch2 := make(chan coretypes.ResultEvent, 10)

	callCount := 0
	var callMu sync.Mutex

	source := newMockEventSource(func(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
		callMu.Lock()
		defer callMu.Unlock()
		callCount++
		if callCount == 1 {
			return ch1, nil
		}
		return ch2, nil
	})

	var stateEvents []StateChangeEvent
	var stateMu sync.Mutex
	stateCallback := func(ev StateChangeEvent) {
		stateMu.Lock()
		defer stateMu.Unlock()
		stateEvents = append(stateEvents, ev)
	}

	logger := zerolog.Nop()
	sub := NewSubscriber(source, logger, 10*time.Millisecond, 50*time.Millisecond, 0, stateCallback)
	sub.heartbeatTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(ctx)
	}()

	// Wait for StateConnected
	require.Eventually(t, func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		return len(stateEvents) >= 1 && stateEvents[0].State == StateConnected
	}, 2*time.Second, 10*time.Millisecond, "expected StateConnected callback")

	// Send event then close channel to trigger disconnect
	ch1 <- makeResultEvent(42, []abci.Event{
		makeSettledABCIEvent("pokt1s", "pokt1a", "svc01"),
	})
	// Drain the event channel
	<-sub.Events()

	close(ch1)

	// Wait for StateDisconnected
	require.Eventually(t, func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		return len(stateEvents) >= 2 && stateEvents[1].State == StateDisconnected
	}, 2*time.Second, 10*time.Millisecond, "expected StateDisconnected callback")

	// Check lastSeenHeight on disconnect
	stateMu.Lock()
	assert.Equal(t, int64(42), stateEvents[1].LastSeenHeight)
	stateMu.Unlock()

	// Wait for StateReconnected
	require.Eventually(t, func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		return len(stateEvents) >= 3 && stateEvents[2].State == StateReconnected
	}, 2*time.Second, 10*time.Millisecond, "expected StateReconnected callback")

	stateMu.Lock()
	assert.Equal(t, int64(42), stateEvents[2].LastSeenHeight)
	stateMu.Unlock()

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestSubscriber_ContextCancellation(t *testing.T) {
	ch := make(chan coretypes.ResultEvent, 10)

	source := newMockEventSource(func(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
		return ch, nil
	})

	logger := zerolog.Nop()
	sub := NewSubscriber(source, logger, 10*time.Millisecond, 50*time.Millisecond, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(ctx)
	}()

	// Allow subscriber to connect
	time.Sleep(50 * time.Millisecond)

	// Cancel immediately
	cancel()

	select {
	case err := <-runDone:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1 second after context cancellation")
	}
}

func TestNextDelay_ExponentialWithJitter(t *testing.T) {
	baseDelay := 1 * time.Second
	maxDelay := 30 * time.Second

	testCases := []struct {
		attempt  int
		minDelay time.Duration // expected center - 20%
		maxDelay time.Duration // expected center + 20%
	}{
		{attempt: 0, minDelay: 800 * time.Millisecond, maxDelay: 1200 * time.Millisecond},     // 1s +/-20%
		{attempt: 1, minDelay: 1600 * time.Millisecond, maxDelay: 2400 * time.Millisecond},    // 2s +/-20%
		{attempt: 2, minDelay: 3200 * time.Millisecond, maxDelay: 4800 * time.Millisecond},    // 4s +/-20%
		{attempt: 3, minDelay: 6400 * time.Millisecond, maxDelay: 9600 * time.Millisecond},    // 8s +/-20%
		{attempt: 4, minDelay: 12800 * time.Millisecond, maxDelay: 19200 * time.Millisecond},  // 16s +/-20%
		{attempt: 10, minDelay: 24000 * time.Millisecond, maxDelay: 36000 * time.Millisecond}, // capped at 30s +/-20%
		{attempt: 20, minDelay: 24000 * time.Millisecond, maxDelay: 36000 * time.Millisecond}, // still capped
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			var seenValues []time.Duration

			for i := 0; i < 100; i++ {
				d := NextDelay(tc.attempt, baseDelay, maxDelay)
				seenValues = append(seenValues, d)

				assert.GreaterOrEqual(t, d, tc.minDelay,
					"attempt %d: delay %v below minimum %v", tc.attempt, d, tc.minDelay)
				assert.LessOrEqual(t, d, tc.maxDelay,
					"attempt %d: delay %v above maximum %v", tc.attempt, d, tc.maxDelay)
			}

			// Verify jitter provides variance (not all identical)
			allSame := true
			for i := 1; i < len(seenValues); i++ {
				if seenValues[i] != seenValues[0] {
					allSame = false
					break
				}
			}
			assert.False(t, allSame, "attempt %d: all 100 delay values were identical (no jitter)", tc.attempt)
		})
	}
}

func TestDecodeBlockResults_ExportedWrapper(t *testing.T) {
	logger := zerolog.Nop()
	stats := NewDecodeStats()
	height := int64(5000)
	blockTime := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	abciEvents := []abci.Event{
		// Valid settled event
		makeSettledABCIEvent("pokt1supplier1", "pokt1app1", "svc01"),
		// Unknown event (should be skipped)
		makeTestEvent("cosmos.bank.v1beta1.EventTransfer", map[string]string{
			"sender": `"pokt1sender"`,
		}),
		// Valid expired event
		makeExpiredABCIEvent("pokt1supplier2", "pokt1app2", "svc02"),
	}

	result := DecodeBlockResults(abciEvents, height, blockTime, logger, stats)

	assert.Equal(t, height, result.Height)
	assert.Equal(t, blockTime, result.Timestamp)
	require.Len(t, result.Events, 2, "expected 2 settlement events (unknown filtered out)")
	assert.Equal(t, proto.MessageName(&tokenomicstypes.EventClaimSettled{}), result.Events[0].EventType)
	assert.Equal(t, proto.MessageName(&tokenomicstypes.EventClaimExpired{}), result.Events[1].EventType)

	// Verify both events have correct height
	for _, ev := range result.Events {
		assert.Equal(t, height, ev.Height)
	}

	// Verify no decode failures
	failures := stats.GetFailures()
	assert.Empty(t, failures, "expected no decode failures for valid events")
}

func TestDecodeBlockResults_EmptyEvents(t *testing.T) {
	logger := zerolog.Nop()
	stats := NewDecodeStats()
	height := int64(5001)
	blockTime := time.Date(2026, 2, 15, 12, 1, 0, 0, time.UTC)

	result := DecodeBlockResults(nil, height, blockTime, logger, stats)

	assert.Equal(t, height, result.Height)
	assert.Equal(t, blockTime, result.Timestamp)
	assert.Empty(t, result.Events)
}

func TestSubscriber_HeartbeatTimeout(t *testing.T) {
	ch1 := make(chan coretypes.ResultEvent, 10)
	ch2 := make(chan coretypes.ResultEvent, 10)

	callCount := 0
	var callMu sync.Mutex

	source := newMockEventSource(func(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
		callMu.Lock()
		defer callMu.Unlock()
		callCount++
		if callCount == 1 {
			return ch1, nil
		}
		return ch2, nil
	})

	var stateEvents []StateChangeEvent
	var stateMu sync.Mutex
	stateCallback := func(ev StateChangeEvent) {
		stateMu.Lock()
		defer stateMu.Unlock()
		stateEvents = append(stateEvents, ev)
	}

	logger := zerolog.Nop()
	sub := NewSubscriber(source, logger, 10*time.Millisecond, 50*time.Millisecond, 0, stateCallback)
	// Very short heartbeat for testing
	sub.heartbeatTimeout = 150 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(ctx)
	}()

	// Send one event then stop sending (simulate stale connection)
	ch1 <- makeResultEvent(200, []abci.Event{
		makeSettledABCIEvent("pokt1s", "pokt1a", "svc01"),
	})
	<-sub.Events()

	// Wait for heartbeat timeout to trigger disconnect + reconnect
	require.Eventually(t, func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		// Should see: Connected, Disconnected (heartbeat), Reconnected
		return len(stateEvents) >= 3
	}, 3*time.Second, 25*time.Millisecond, "expected heartbeat-triggered disconnect and reconnect")

	stateMu.Lock()
	assert.Equal(t, StateConnected, stateEvents[0].State)
	assert.Equal(t, StateDisconnected, stateEvents[1].State)
	assert.Equal(t, int64(200), stateEvents[1].LastSeenHeight)
	assert.Equal(t, StateReconnected, stateEvents[2].State)
	stateMu.Unlock()

	// Verify reconnected subscriber works by sending on ch2
	ch2 <- makeResultEvent(210, []abci.Event{
		makeSettledABCIEvent("pokt1s2", "pokt1a2", "svc02"),
	})

	select {
	case blockEvents := <-sub.Events():
		assert.Equal(t, int64(210), blockEvents.Height)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event after heartbeat reconnection")
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}
