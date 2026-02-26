package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"

	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// recordedBlock captures a single ProcessBlock call for test assertions.
type recordedBlock struct {
	Height    int64
	Timestamp time.Time
	Events    []subscriber.SettlementEvent
	IsLive    bool
}

// recordingProcessor is a real implementation of BlockProcessor that records
// all ProcessBlock calls for assertion. NOT a mock -- just a simple capture
// implementation per CLAUDE.md no-mocks rule.
type recordingProcessor struct {
	mu     sync.Mutex
	blocks []recordedBlock
}

func (r *recordingProcessor) ProcessBlock(_ context.Context, height int64, ts time.Time, events []subscriber.SettlementEvent, isLive bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy events to avoid mutation after recording.
	eventsCopy := make([]subscriber.SettlementEvent, len(events))
	copy(eventsCopy, events)
	r.blocks = append(r.blocks, recordedBlock{
		Height:    height,
		Timestamp: ts,
		Events:    eventsCopy,
		IsLive:    isLive,
	})
	return nil
}

func (r *recordingProcessor) getBlocks() []recordedBlock {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]recordedBlock, len(r.blocks))
	copy(result, r.blocks)
	return result
}

func makeSettledEvent(supplier string) *tokenomicstypes.EventClaimSettled {
	return &tokenomicstypes.EventClaimSettled{
		SupplierOperatorAddress: supplier,
		ApplicationAddress:      "pokt1app",
		ServiceId:               "svc1",
		ClaimedUpokt:            "1000upokt",
	}
}

func TestCollector_HeightChangeFlush(t *testing.T) {
	rec := &recordingProcessor{}
	c := NewCollector(rec, zerolog.Nop())
	c.SetFlushTimeout(5 * time.Second) // Long timeout -- should not trigger.

	ch := make(chan subscriber.BlockEvents, 10)

	// Send 2 BlockEvents with different heights.
	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: testTime,
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "settled", Event: makeSettledEvent("pokt1a")},
		},
	}
	ch <- subscriber.BlockEvents{
		Height:    101,
		Timestamp: testTime.Add(time.Minute),
		Events: []subscriber.SettlementEvent{
			{Height: 101, EventType: "settled", Event: makeSettledEvent("pokt1b")},
		},
	}

	// Close channel to trigger final flush of height 101.
	close(ch)

	err := c.Run(context.Background(), ch)
	require.NoError(t, err)

	blocks := rec.getBlocks()
	require.Len(t, blocks, 2, "expected 2 flushes: height change + channel close")

	// First flush: height 100 (triggered by height change to 101).
	assert.Equal(t, int64(100), blocks[0].Height)
	assert.Equal(t, testTime, blocks[0].Timestamp)
	assert.Len(t, blocks[0].Events, 1)
	assert.True(t, blocks[0].IsLive)

	// Second flush: height 101 (triggered by channel close).
	assert.Equal(t, int64(101), blocks[1].Height)
	assert.Len(t, blocks[1].Events, 1)
}

func TestCollector_SafetyTimeout(t *testing.T) {
	rec := &recordingProcessor{}
	c := NewCollector(rec, zerolog.Nop())
	c.SetFlushTimeout(50 * time.Millisecond) // Short timeout for test.

	ch := make(chan subscriber.BlockEvents, 10)

	// Send 1 BlockEvents, then wait for safety flush.
	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "settled", Event: makeSettledEvent("pokt1a")},
		},
	}

	// Run the collector in a goroutine; let the safety timeout fire, then close the channel.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, ch)
	}()

	// Wait for the safety flush to fire.
	time.Sleep(200 * time.Millisecond)

	blocks := rec.getBlocks()
	require.Len(t, blocks, 1, "expected 1 flush from safety timeout")
	assert.Equal(t, int64(100), blocks[0].Height)
	assert.Len(t, blocks[0].Events, 1)

	// Clean up: cancel context to stop the collector.
	cancel()
	<-done
}

func TestCollector_ContextCancellation(t *testing.T) {
	rec := &recordingProcessor{}
	c := NewCollector(rec, zerolog.Nop())
	c.SetFlushTimeout(10 * time.Second) // Long timeout -- should not trigger.

	ch := make(chan subscriber.BlockEvents, 10)

	// Send 1 BlockEvents.
	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "settled", Event: makeSettledEvent("pokt1a")},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, ch)
	}()

	// Give the collector time to read the event.
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger final flush.
	cancel()
	err := <-done
	assert.ErrorIs(t, err, context.Canceled)

	blocks := rec.getBlocks()
	require.Len(t, blocks, 1, "expected 1 flush from context cancellation")
	assert.Equal(t, int64(100), blocks[0].Height)
	assert.Len(t, blocks[0].Events, 1)
}

func TestCollector_ChannelClose(t *testing.T) {
	rec := &recordingProcessor{}
	c := NewCollector(rec, zerolog.Nop())
	c.SetFlushTimeout(10 * time.Second)

	ch := make(chan subscriber.BlockEvents, 10)

	// Send 1 BlockEvents and close the channel immediately.
	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "settled", Event: makeSettledEvent("pokt1a")},
		},
	}
	close(ch)

	err := c.Run(context.Background(), ch)
	require.NoError(t, err)

	blocks := rec.getBlocks()
	require.Len(t, blocks, 1, "expected 1 flush from channel close")
	assert.Equal(t, int64(100), blocks[0].Height)
	assert.Len(t, blocks[0].Events, 1)
}

func TestCollector_AccumulatesEventsForSameHeight(t *testing.T) {
	rec := &recordingProcessor{}
	c := NewCollector(rec, zerolog.Nop())
	c.SetFlushTimeout(10 * time.Second)

	ch := make(chan subscriber.BlockEvents, 10)

	// Send 2 BlockEvents at the same height.
	testTime := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: testTime,
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "settled", Event: makeSettledEvent("pokt1a")},
		},
	}
	ch <- subscriber.BlockEvents{
		Height:    100,
		Timestamp: testTime,
		Events: []subscriber.SettlementEvent{
			{Height: 100, EventType: "expired", Event: makeSettledEvent("pokt1b")},
			{Height: 100, EventType: "slashed", Event: makeSettledEvent("pokt1c")},
		},
	}

	// Close channel to trigger flush.
	close(ch)

	err := c.Run(context.Background(), ch)
	require.NoError(t, err)

	blocks := rec.getBlocks()
	require.Len(t, blocks, 1, "expected 1 flush with all accumulated events")
	assert.Equal(t, int64(100), blocks[0].Height)
	assert.Len(t, blocks[0].Events, 3, "expected 3 accumulated events")
}
