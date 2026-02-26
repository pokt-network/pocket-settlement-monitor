package cmd

import (
	"fmt"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/processor"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

func init() {
	rootCmd.AddCommand(backfillCmd)

	backfillCmd.Flags().String("from", "", "start height or date (required)")
	backfillCmd.Flags().String("to", "", "end height or date (required)")
	backfillCmd.Flags().Duration("delay", 100*time.Millisecond, "delay between block fetches")

	_ = backfillCmd.MarkFlagRequired("from")
	_ = backfillCmd.MarkFlagRequired("to")
}

var backfillCmd = &cobra.Command{
	Use:   "backfill",
	Short: "Backfill historical settlement data from a CometBFT node",
	Long: `Fetches historical block_results for a specified height range and writes
settlement events to SQLite. Accepts both block heights and ISO 8601 dates
for --from and --to flags. Dates are resolved to heights via binary search.

Prometheus counters are NOT incremented during backfill -- only SQLite writes.
After completion, hourly and daily summaries are recalculated for the range.`,
	RunE: runBackfill,
}

// runBackfill implements the backfill command logic.
func runBackfill(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// 1. Setup: Logger from root, component logger.
	log := logging.ForComponent(logger, "backfill-cmd")
	log.Info().Msg("starting backfill command")

	// 2. Open SQLite store.
	db, err := store.Open(ctx, cfg.Database.Path, cfg.Database.Retention, logger)
	if err != nil {
		return fmt.Errorf("opening SQLite store: %w", err)
	}
	defer db.Close()

	// 3. Create RPC client (separate from any subscriber's client).
	client, err := rpchttp.New(cfg.CometBFT.RPCURL, "/websocket")
	if err != nil {
		return fmt.Errorf("creating RPC client: %w", err)
	}
	if startErr := client.Start(); startErr != nil {
		return fmt.Errorf("starting RPC client: %w", startErr)
	}
	defer func() { _ = client.Stop() }()

	// 4. Resolve heights from flags.
	fromFlag, _ := cmd.Flags().GetString("from")
	toFlag, _ := cmd.Flags().GetString("to")
	delayFlag, _ := cmd.Flags().GetDuration("delay")

	fromHeight, err := resolveFromHeight(ctx, client, fromFlag)
	if err != nil {
		return fmt.Errorf("resolving --from: %w", err)
	}
	log.Info().Str("input", fromFlag).Int64("height", fromHeight).Msg("--from resolved")

	toHeight, err := resolveToHeight(ctx, client, toFlag)
	if err != nil {
		return fmt.Errorf("resolving --to: %w", err)
	}
	log.Info().Str("input", toFlag).Int64("height", toHeight).Msg("--to resolved")

	// 5. Validate range.
	if fromHeight > toHeight {
		return fmt.Errorf("invalid range: --from (%d) must be <= --to (%d)", fromHeight, toHeight)
	}

	status, err := client.Status(ctx)
	if err != nil {
		return fmt.Errorf("querying chain status: %w", err)
	}
	currentHeight := status.SyncInfo.LatestBlockHeight
	if toHeight > currentHeight {
		return fmt.Errorf("invalid range: --to (%d) exceeds current chain height (%d)", toHeight, currentHeight)
	}

	log.Info().
		Int64("from", fromHeight).
		Int64("to", toHeight).
		Dur("delay", delayFlag).
		Msg("backfill range resolved")

	// 6. Create processor with isolated Prometheus registry (no-op sink, never exposed via HTTP).
	registry := prometheus.NewRegistry()
	labelCfg := &metrics.LabelConfig{}
	m := metrics.NewMetrics(registry, labelCfg)

	// Resolve supplier addresses and create filter (consistent with monitor behavior).
	addresses, err := config.LoadSupplierAddresses(cfg.Suppliers)
	if err != nil {
		return fmt.Errorf("loading supplier addresses: %w", err)
	}
	filter := processor.NewSupplierFilter(addresses, logger)
	proc := processor.NewProcessor(db, m, filter, logger)

	// Create reporter and wire to processor for summary recalculation.
	reporter, err := processor.NewReporter(ctx, db, logger)
	if err != nil {
		return fmt.Errorf("creating reporter: %w", err)
	}
	proc.SetReporter(reporter)

	// 7. Sequential backfill loop.
	backfillStart := time.Now()
	var firstBlockTime, lastBlockTime time.Time
	var totalEvents int64
	stats := subscriber.NewDecodeStats()
	totalBlocks := toHeight - fromHeight + 1

	for h := fromHeight; h <= toHeight; h++ {
		// Check for shutdown between blocks.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Retry wrapper: max 10 retries per block.
		var blockTime time.Time
		var blockEventsDecoded subscriber.BlockEvents
		var fetchErr error

		for attempt := 0; attempt <= 10; attempt++ {
			// Backoff on retry.
			if attempt > 0 {
				delay := subscriber.NextDelay(attempt-1, 1*time.Second, 30*time.Second)
				log.Warn().
					Int("attempt", attempt).
					Int64("height", h).
					Dur("delay", delay).
					Msg("retrying block fetch")

				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			// Fetch block results.
			hp := h
			results, err := client.BlockResults(ctx, &hp)
			if err != nil {
				fetchErr = err
				if attempt == 10 {
					return fmt.Errorf("failed after 10 retries for height %d (last success: %d): %w", h, h-1, fetchErr)
				}
				continue
			}

			// Get block timestamp from header.
			headerResult, err := client.Header(ctx, &hp)
			if err != nil {
				fetchErr = err
				if attempt == 10 {
					return fmt.Errorf("failed after 10 retries for height %d (last success: %d): %w", h, h-1, fetchErr)
				}
				continue
			}

			blockTime = headerResult.Header.Time

			// Decode events.
			blockEventsDecoded = subscriber.DecodeBlockResults(results.FinalizeBlockEvents, h, blockTime, logger, stats)
			fetchErr = nil
			break
		}

		// Track first and last block times.
		if h == fromHeight {
			firstBlockTime = blockTime
		}
		lastBlockTime = blockTime

		// Process with isLive=false (no Prometheus counters, BKFL-03/BKFL-04).
		if err := proc.ProcessBlock(ctx, h, blockTime, blockEventsDecoded.Events, false); err != nil {
			return fmt.Errorf("processing backfill block %d: %w", h, err)
		}

		// Track total events.
		totalEvents += int64(len(blockEventsDecoded.Events))

		// Progress logging every 100 blocks or on last block (BKFL-06).
		processed := h - fromHeight + 1
		if processed%100 == 0 || h == toHeight {
			pct := float64(processed) / float64(totalBlocks) * 100
			log.Info().
				Int64("processed", processed).
				Int64("total", totalBlocks).
				Float64("percent", pct).
				Msgf("Backfill: %d/%d blocks processed (%.0f%%)", processed, totalBlocks, pct)
		}

		// Delay between blocks.
		if delayFlag > 0 {
			select {
			case <-time.After(delayFlag):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// 8. Summary recalculation (BKFL-05).
	summariesOK := true
	if err := reporter.RecalculateSummariesForRange(ctx, firstBlockTime, lastBlockTime); err != nil {
		log.Error().Err(err).Msg("summary recalculation failed after backfill (non-fatal, data is in DB)")
		summariesOK = false
	} else {
		log.Info().Msg("summary recalculation complete")
	}

	// 9. Completion summary.
	elapsed := time.Since(backfillStart)
	log.Info().
		Int64("blocks_processed", totalBlocks).
		Int64("events_found", totalEvents).
		Dur("elapsed", elapsed).
		Int64("from_height", fromHeight).
		Int64("to_height", toHeight).
		Bool("summaries_recalculated", summariesOK).
		Msg("backfill complete")

	// Print human-readable summary to stdout.
	fmt.Printf("Backfill complete: %d blocks processed (%d-%d), %d events found, %s elapsed\n",
		totalBlocks, fromHeight, toHeight, totalEvents, elapsed.Round(time.Millisecond))

	return nil
}
