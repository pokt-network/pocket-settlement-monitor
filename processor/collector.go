package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// BlockProcessor defines the interface for processing a batch of events for a single block.
// The real *Processor satisfies this interface.
type BlockProcessor interface {
	ProcessBlock(ctx context.Context, height int64, ts time.Time, events []subscriber.SettlementEvent, isLive bool) error
}

// Collector reads BlockEvents from the subscriber channel, accumulates events per block
// height, and flushes to the BlockProcessor when:
//   - A new height arrives (height-change flush)
//   - No new blocks arrive within flushTimeout (safety flush, default 60s)
//   - The context is canceled (graceful shutdown flush)
//   - The events channel is closed (final flush)
type Collector struct {
	processor        BlockProcessor
	logger           zerolog.Logger
	currentHeight    int64
	currentTimestamp time.Time
	currentEvents    []subscriber.SettlementEvent
	flushTimeout     time.Duration
	isLive           bool
}

// NewCollector creates a Collector with default settings (60s flush timeout, isLive=true).
func NewCollector(processor BlockProcessor, logger zerolog.Logger) *Collector {
	return &Collector{
		processor:    processor,
		logger:       logger,
		flushTimeout: 60 * time.Second,
		isLive:       true,
	}
}

// SetFlushTimeout overrides the default 60s safety flush timeout.
// Useful for tests that need a shorter timeout.
func (c *Collector) SetFlushTimeout(d time.Duration) {
	c.flushTimeout = d
}

// SetIsLive sets whether events should be treated as live (true) or backfill (false).
func (c *Collector) SetIsLive(isLive bool) {
	c.isLive = isLive
}

// Run reads from the events channel and processes blocks.
// It returns ctx.Err() on context cancellation, or nil when the channel is closed.
func (c *Collector) Run(ctx context.Context, events <-chan subscriber.BlockEvents) error {
	timer := time.NewTimer(c.flushTimeout)
	// Stop the timer initially -- it only becomes active after the first event.
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Final flush on context cancellation. Use a fresh context with
			// a deadline so the flush can complete even though ctx is canceled.
			timer.Stop()
			if len(c.currentEvents) > 0 {
				flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := c.flush(flushCtx); err != nil {
					c.logger.Error().Err(err).Int64("height", c.currentHeight).Msg("error during final flush on context cancellation")
				}
				cancel()
			}
			return ctx.Err()

		case block, ok := <-events:
			if !ok {
				// Channel closed -- final flush.
				timer.Stop()
				if len(c.currentEvents) > 0 {
					if err := c.flush(context.Background()); err != nil {
						c.logger.Error().Err(err).Int64("height", c.currentHeight).Msg("error during final flush on channel close")
					}
				}
				return nil
			}

			// Height change: flush the previous block.
			if c.currentHeight != 0 && block.Height != c.currentHeight {
				if err := c.flush(ctx); err != nil {
					c.logger.Error().Err(err).Int64("height", c.currentHeight).Msg("error flushing block on height change")
					return fmt.Errorf("flushing block %d: %w", c.currentHeight, err)
				}
			}

			// Accumulate events for this height.
			c.currentHeight = block.Height
			c.currentTimestamp = block.Timestamp
			c.currentEvents = append(c.currentEvents, block.Events...)

			// Reset the safety timer (stop + drain + reset pattern to avoid race).
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.flushTimeout)

		case <-timer.C:
			// Safety flush: no new block arrived within flushTimeout.
			if len(c.currentEvents) > 0 {
				if err := c.flush(ctx); err != nil {
					c.logger.Error().Err(err).Int64("height", c.currentHeight).Msg("error during safety timeout flush")
					return fmt.Errorf("safety flush block %d: %w", c.currentHeight, err)
				}
			}
		}
	}
}

// flush sends accumulated events to the processor and resets state.
func (c *Collector) flush(ctx context.Context) error {
	err := c.processor.ProcessBlock(ctx, c.currentHeight, c.currentTimestamp, c.currentEvents, c.isLive)
	if err != nil {
		c.logger.Error().Err(err).
			Int64("height", c.currentHeight).
			Int("events", len(c.currentEvents)).
			Msg("ProcessBlock failed")
		// Reset state even on error to avoid reprocessing stale data.
	}
	c.currentEvents = nil
	c.currentHeight = 0
	c.currentTimestamp = time.Time{}
	if err != nil {
		return fmt.Errorf("processing block: %w", err)
	}
	return nil
}
