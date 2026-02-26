package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// noopProcessor is a real implementation of processor.BlockProcessor that does nothing.
// NOT a mock -- just a minimal implementation per CLAUDE.md no-mocks rule.
type noopProcessor struct{}

func (n *noopProcessor) ProcessBlock(_ context.Context, _ int64, _ time.Time, _ []subscriber.SettlementEvent, _ bool) error {
	return nil
}

func TestBlockCountingProcessor_CountsAndReadiness(t *testing.T) {
	// 1. Create isolated registry and metrics.
	registry := prometheus.NewRegistry()
	m := metrics.NewMetrics(registry, nil)

	// 2. Create a minimal metrics server (needed because firstBlockOnce.Do calls SetWSConnected).
	metricsServer := metrics.NewServer("127.0.0.1:0", registry, zerolog.Nop())

	// 3. Create a Monitor with just the fields needed by blockCountingProcessor.
	mon := &Monitor{
		metrics:       m,
		metricsServer: metricsServer,
		logger:        zerolog.Nop(),
	}

	// 4. Create blockCountingProcessor wrapping a no-op inner processor.
	wrapper := &blockCountingProcessor{inner: &noopProcessor{}, monitor: mon}

	ctx := context.Background()
	ts := time.Now()

	// 5. Process a live block.
	err := wrapper.ProcessBlock(ctx, 100, ts, nil, true)
	require.NoError(t, err)

	// 6. Process a backfill block.
	err = wrapper.ProcessBlock(ctx, 50, ts, nil, false)
	require.NoError(t, err)

	// 7. Process another live block.
	err = wrapper.ProcessBlock(ctx, 101, ts, nil, true)
	require.NoError(t, err)

	// 8. Verify blocksProcessed and eventsProcessed counters increment for ALL calls (live + backfill).
	assert.Equal(t, int64(3), mon.blocksProcessed.Load(), "blocksProcessed should count all blocks (live + backfill)")
	assert.Equal(t, int64(0), mon.eventsProcessed.Load(), "eventsProcessed should be 0 when no events passed")

	// 9. Process a block with events to verify eventsProcessed.
	events := make([]subscriber.SettlementEvent, 5)
	err = wrapper.ProcessBlock(ctx, 102, ts, events, true)
	require.NoError(t, err)

	assert.Equal(t, int64(4), mon.blocksProcessed.Load(), "blocksProcessed should be 4 after 4th block")
	assert.Equal(t, int64(5), mon.eventsProcessed.Load(), "eventsProcessed should count the 5 events")

	// Note: psm_current_block_height is now set by the subscriber's OnBlock callback
	// (fires on every WebSocket block), not by blockCountingProcessor.
}
