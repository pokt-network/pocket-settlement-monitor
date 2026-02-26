package cmd

import (
	"context"
	"fmt"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/rs/zerolog"

	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/processor"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// Backfiller detects gaps between the last processed height and the current
// chain height, then sequentially fetches and processes missed blocks. It uses
// its OWN RPC client (separate from the subscriber's) per research recommendation.
type Backfiller struct {
	rpcURL               string
	processor            *processor.Processor
	reporter             *processor.Reporter
	store                store.Store
	metrics              *metrics.Metrics
	opsNotifier          *OpsNotifier
	logger               zerolog.Logger
	delay                time.Duration // between requests (default 100ms)
	progressN            int           // log every N blocks (default 100)
	initialFrom          string        // --from flag: height or date string, empty means no initial backfill
	liveCatchupThreshold time.Duration // blocks newer than this are treated as live during gap recovery
}

// NewBackfiller creates a Backfiller configured to query the given RPC URL.
func NewBackfiller(
	rpcURL string,
	proc *processor.Processor,
	rep *processor.Reporter,
	st store.Store,
	m *metrics.Metrics,
	ops *OpsNotifier,
	logger zerolog.Logger,
	delay time.Duration,
	progressN int,
	initialFrom string,
	liveCatchupThreshold time.Duration,
) *Backfiller {
	if progressN <= 0 {
		progressN = 100
	}
	return &Backfiller{
		rpcURL:               rpcURL,
		processor:            proc,
		reporter:             rep,
		store:                st,
		metrics:              m,
		opsNotifier:          ops,
		logger:               logging.ForComponent(logger, "backfiller"),
		delay:                delay,
		progressN:            progressN,
		initialFrom:          initialFrom,
		liveCatchupThreshold: liveCatchupThreshold,
	}
}

// RunIfNeeded checks for a gap and backfills if necessary. Called in an errgroup
// goroutine from Monitor.Run(). Returns nil if no backfill is needed.
func (b *Backfiller) RunIfNeeded(ctx context.Context) error {
	// Create our own RPC client (separate from subscriber's per research).
	client, err := rpchttp.New(b.rpcURL, "/websocket")
	if err != nil {
		return fmt.Errorf("creating backfill RPC client: %w", err)
	}
	if startErr := client.Start(); startErr != nil {
		return fmt.Errorf("starting backfill RPC client: %w", startErr)
	}
	defer func() { _ = client.Stop() }()

	// Query last processed height from store.
	lastHeight, err := b.store.LastProcessedHeight(ctx)
	if err != nil {
		return fmt.Errorf("querying last processed height: %w", err)
	}

	// Query current chain height.
	status, err := client.Status(ctx)
	if err != nil {
		return fmt.Errorf("querying chain status: %w", err)
	}
	currentHeight := status.SyncInfo.LatestBlockHeight

	// Resolve --from flag if set (supports both heights and dates).
	var requestedFrom int64
	if b.initialFrom != "" {
		requestedFrom, err = resolveToHeight(ctx, client, b.initialFrom)
		if err != nil {
			return fmt.Errorf("resolving --from %q: %w", b.initialFrom, err)
		}
		b.logger.Info().
			Int64("requested_from", requestedFrom).
			Str("raw_flag", b.initialFrom).
			Msg("--from flag resolved")
	}

	// Determine backfill start height.
	// Priority: use --from when it extends further back than what the DB has.
	fromHeight := lastHeight + 1
	switch {
	case lastHeight == 0 && requestedFrom == 0:
		// Fresh DB, no --from: start from current height (no backfill).
		b.logger.Info().
			Int64("current_height", currentHeight).
			Msg("first run -- starting from current height (use --from for historical backfill)")
		return nil
	case lastHeight == 0 && requestedFrom > 0:
		// Fresh DB with --from: backfill from requested height.
		fromHeight = requestedFrom
	case requestedFrom > 0 && requestedFrom < lastHeight:
		// Existing DB but --from is earlier: extend history backwards.
		// INSERT OR IGNORE handles overlap with already-processed blocks.
		fromHeight = requestedFrom
		b.logger.Info().
			Int64("last_processed", lastHeight).
			Int64("extending_from", requestedFrom).
			Msg("--from extends history before existing data")
	}

	toHeight := currentHeight

	b.logger.Info().
		Int64("last_processed", lastHeight).
		Int64("current_height", currentHeight).
		Int64("from_height", fromHeight).
		Msg("checking for gap")

	// No gap.
	if fromHeight > toHeight {
		b.logger.Info().Msg("no gap detected, skipping backfill")
		return nil
	}

	gap := toHeight - fromHeight + 1

	b.logger.Warn().
		Int64("gap", gap).
		Int64("from_height", fromHeight).
		Int64("to_height", toHeight).
		Msg("gap detected, starting backfill")

	b.metrics.IncrementGapDetected(true)
	b.metrics.SetBackfillBlocksRemaining(float64(gap))
	b.opsNotifier.SendGapDetected(fromHeight, toHeight, gap)
	b.opsNotifier.SendBackfillStarted(fromHeight, toHeight)

	// Sequential backfill loop.
	backfillStart := time.Now()
	stats := subscriber.NewDecodeStats()
	var firstBlockTime, lastBlockTime time.Time

	for h := fromHeight; h <= toHeight; h++ {
		// Check for shutdown between blocks (not mid-block) per locked decision.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch block results.
		hp := h
		results, err := client.BlockResults(ctx, &hp)
		if err != nil {
			return fmt.Errorf("fetching block results for height %d: %w", h, err)
		}

		// Get block timestamp from header.
		headerResult, err := client.Header(ctx, &hp)
		var blockTime time.Time
		if err != nil {
			b.logger.Warn().Err(err).Int64("height", h).
				Msg("failed to fetch header for backfill block, using time.Now()")
			blockTime = time.Now()
		} else {
			blockTime = headerResult.Header.Time
		}

		// Track first and last block times for summary recalculation range.
		if h == fromHeight {
			firstBlockTime = blockTime
		}
		lastBlockTime = blockTime

		// Decode events using exported DecodeBlockResults.
		blockEvents := subscriber.DecodeBlockResults(results.FinalizeBlockEvents, h, blockTime, b.logger, stats)

		// Process with isLive=false (MNTR-06: INSERT OR IGNORE deduplication).
		if err := b.processor.ProcessBlock(ctx, h, blockTime, blockEvents.Events, false); err != nil {
			return fmt.Errorf("processing backfill block %d: %w", h, err)
		}

		// Progress logging per locked decision: "Backfill: 150/500 blocks processed (30%)"
		processed := h - fromHeight + 1
		total := toHeight - fromHeight + 1
		if processed%int64(b.progressN) == 0 || h == toHeight {
			pct := float64(processed) / float64(total) * 100
			b.logger.Info().
				Int64("processed", processed).
				Int64("total", total).
				Float64("percent", pct).
				Msgf("Backfill: %d/%d blocks processed (%.0f%%)", processed, total, pct)
		}

		// Update remaining gauge.
		b.metrics.SetBackfillBlocksRemaining(float64(toHeight - h))

		// Throttle per locked decision.
		if b.delay > 0 {
			select {
			case <-time.After(b.delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Backfill complete.
	b.metrics.SetBackfillBlocksRemaining(0)

	// Recalculate summaries for backfilled range per locked decision.
	b.logger.Info().
		Time("from", firstBlockTime).
		Time("to", lastBlockTime).
		Msg("recalculating summaries for backfilled range")

	if err := b.reporter.RecalculateSummariesForRange(ctx, firstBlockTime, lastBlockTime); err != nil {
		b.logger.Error().Err(err).Msg("summary recalculation failed after backfill")
		// Non-fatal: backfill data is in the DB, summaries can be recalculated later.
	} else {
		b.logger.Info().Msg("summary recalculation complete")
	}

	duration := time.Since(backfillStart)
	totalBlocks := toHeight - fromHeight + 1
	b.opsNotifier.SendBackfillCompleted(fromHeight, toHeight, duration)
	b.logger.Info().
		Int64("blocks", totalBlocks).
		Dur("duration", duration).
		Msg("backfill complete")

	b.logger.Info().
		Int64("last_backfilled_height", toHeight).
		Msg("live monitoring active -- waiting for settlement events from WebSocket")

	return nil
}

// BackfillRange backfills a specific range of blocks, typically triggered by
// the subscriber detecting a height gap after reconnection. It creates its own
// RPC client and runs the backfill loop for the given range.
//
// If liveCatchupThreshold is configured, blocks whose timestamp is within that
// duration of wall-clock time are processed as isLive=true — incrementing
// Prometheus counters and firing Discord notifications. This handles the case
// where CometBFT fails to deliver settlement blocks via WebSocket (they are
// too heavy) but they are only a few minutes old.
//
// Runs asynchronously (called from subscriber goroutine) — errors are logged,
// not returned.
func (b *Backfiller) BackfillRange(fromHeight, toHeight int64) {
	gap := toHeight - fromHeight + 1

	b.logger.Warn().
		Int64("gap", gap).
		Int64("from_height", fromHeight).
		Int64("to_height", toHeight).
		Msg("gap detected on reconnect, starting backfill")

	b.metrics.IncrementGapDetected(true)
	b.metrics.SetBackfillBlocksRemaining(float64(gap))
	b.opsNotifier.SendGapDetected(fromHeight, toHeight, gap)
	b.opsNotifier.SendBackfillStarted(fromHeight, toHeight)

	// Create a dedicated RPC client for this backfill.
	client, err := rpchttp.New(b.rpcURL, "/websocket")
	if err != nil {
		b.logger.Error().Err(err).Msg("failed to create backfill RPC client for gap recovery")
		b.metrics.SetBackfillBlocksRemaining(0)
		return
	}
	if startErr := client.Start(); startErr != nil {
		b.logger.Error().Err(startErr).Msg("failed to start backfill RPC client for gap recovery")
		b.metrics.SetBackfillBlocksRemaining(0)
		return
	}
	defer func() { _ = client.Stop() }()

	ctx := context.Background()
	backfillStart := time.Now()
	stats := subscriber.NewDecodeStats()
	var firstBlockTime, lastBlockTime time.Time

	for h := fromHeight; h <= toHeight; h++ {
		hp := h
		results, err := client.BlockResults(ctx, &hp)
		if err != nil {
			b.logger.Error().Err(err).Int64("height", h).Msg("failed to fetch block during gap recovery")
			b.metrics.SetBackfillBlocksRemaining(0)
			return
		}

		headerResult, err := client.Header(ctx, &hp)
		var blockTime time.Time
		if err != nil {
			b.logger.Warn().Err(err).Int64("height", h).
				Msg("failed to fetch header during gap recovery, using time.Now()")
			blockTime = time.Now()
		} else {
			blockTime = headerResult.Header.Time
		}

		if h == fromHeight {
			firstBlockTime = blockTime
		}
		lastBlockTime = blockTime

		blockEvents := subscriber.DecodeBlockResults(results.FinalizeBlockEvents, h, blockTime, b.logger, stats)

		// Determine if this block should be treated as live. If the block
		// is recent enough (within liveCatchupThreshold), it was only missed
		// because CometBFT failed to deliver it — treat it as live so
		// Prometheus counters and Discord notifications fire.
		isLive := b.liveCatchupThreshold > 0 && time.Since(blockTime) <= b.liveCatchupThreshold

		if isLive {
			b.logger.Info().
				Int64("height", h).
				Dur("block_age", time.Since(blockTime)).
				Dur("threshold", b.liveCatchupThreshold).
				Msg("gap block within live catchup threshold, treating as live")
		}

		if err := b.processor.ProcessBlock(ctx, h, blockTime, blockEvents.Events, isLive); err != nil {
			b.logger.Error().Err(err).Int64("height", h).Msg("failed to process block during gap recovery")
			b.metrics.SetBackfillBlocksRemaining(0)
			return
		}

		b.metrics.SetBackfillBlocksRemaining(float64(toHeight - h))

		if b.delay > 0 {
			time.Sleep(b.delay)
		}
	}

	b.metrics.SetBackfillBlocksRemaining(0)

	// Recalculate summaries for the gap range.
	if err := b.reporter.RecalculateSummariesForRange(ctx, firstBlockTime, lastBlockTime); err != nil {
		b.logger.Error().Err(err).Msg("summary recalculation failed after gap recovery")
	}

	duration := time.Since(backfillStart)
	b.opsNotifier.SendBackfillCompleted(fromHeight, toHeight, duration)
	b.logger.Info().
		Int64("blocks", gap).
		Dur("duration", duration).
		Msg("gap recovery backfill complete")
}
