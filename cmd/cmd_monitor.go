package cmd

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/internal/version"
	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/notify"
	"github.com/pokt-network/pocket-settlement-monitor/processor"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

func init() {
	rootCmd.AddCommand(monitorCmd)

	monitorCmd.Flags().String("from", "", "backfill from this height or date on startup (e.g., 500000 or 2024-02-15)")
}

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Start the settlement event monitor",
	Long: `Starts the full settlement monitoring pipeline: connects to CometBFT
WebSocket, processes settlement events, persists to SQLite, exposes Prometheus
metrics, and sends Discord notifications.`,
	RunE: runMonitor,
}

// runMonitor wires all components in dependency order and starts the monitor.
func runMonitor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// 1. Logger (already available from root.go PersistentPreRunE).
	log := logging.ForComponent(logger, "monitor")
	log.Info().Msg("initializing settlement monitor")

	// 2. Open SQLite store.
	db, err := store.Open(ctx, cfg.Database.Path, cfg.Database.Retention, logger)
	if err != nil {
		return fmt.Errorf("opening SQLite store: %w", err)
	}
	log.Info().Str("path", cfg.Database.Path).Msg("SQLite opened")

	// Track whether we need to clean up on error.
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()

	// 3. Create Prometheus registry and metrics.
	registry := prometheus.NewRegistry()
	labelCfg := &metrics.LabelConfig{
		IncludeSupplier:    cfg.Metrics.Labels.IncludeSupplier,
		IncludeService:     cfg.Metrics.Labels.IncludeService,
		IncludeApplication: cfg.Metrics.Labels.IncludeApplication,
	}
	m := metrics.NewMetrics(registry, labelCfg)
	m.SetInfo(version.Version, version.Commit)
	db.SetMetrics(m)
	log.Info().Msg("Prometheus metrics registered")

	// 4. Create metrics server (started inside Monitor.Run via errgroup).
	metricsServer := metrics.NewServer(cfg.Metrics.Addr, registry, logger)
	// Wire DB check for /ready endpoint.
	metricsServer.SetDBCheck(func() bool {
		_, dbErr := db.LastProcessedHeight(ctx)
		return dbErr == nil
	})

	// 5. Resolve supplier addresses and create filter.
	addresses, err := config.LoadSupplierAddresses(cfg.Suppliers)
	if err != nil {
		return fmt.Errorf("loading supplier addresses: %w", err)
	}
	filter := processor.NewSupplierFilter(addresses, logger)
	if len(addresses) > 0 {
		log.Info().Int("count", len(addresses)).Msg("supplier filter active")
		m.SetSuppliersMonitored(float64(len(addresses)))
	} else {
		log.Info().Msg("monitor-all mode: no supplier filter")
	}

	// 6. Create processor.
	proc := processor.NewProcessor(db, m, filter, logger)

	// 7. Create reporter and wire to processor.
	reporter, err := processor.NewReporter(ctx, db, logger)
	if err != nil {
		return fmt.Errorf("creating reporter: %w", err)
	}
	proc.SetReporter(reporter)
	log.Info().Msg("reporter initialized")

	// 8. Create notifier and wire to processor (Start() called inside Monitor.Run).
	notif := notify.NewNotifier(cfg.Notifications, m, db, logger)
	proc.SetNotifier(notif)
	if cfg.Notifications.WebhookURL != "" {
		log.Info().Msg("Discord notifications enabled")
	}

	// 9. Create CometBFT source.
	source := subscriber.NewCometBFTSource(cfg.CometBFT.RPCURL)
	log.Info().Str("rpc_url", cfg.CometBFT.RPCURL).Msg("CometBFT source configured")

	// 10. Create OpsNotifier.
	opsNotifier := NewOpsNotifier(cfg.Notifications, logger)
	if cfg.Notifications.EffectiveOpsWebhookURL() != "" {
		log.Info().Msg("operational Discord notifications enabled")
	}

	// 11. Create state change callback (wires subscriber state to metrics + readiness + ops notifications).
	metricsCB := metrics.NewStateChangeCallback(m, metricsServer)
	stateChangeCB := func(e subscriber.StateChangeEvent) {
		metricsCB(e)
		switch e.State {
		case subscriber.StateConnected:
			opsNotifier.SendWebSocketConnected(cfg.CometBFT.RPCURL)
		case subscriber.StateDisconnected:
			opsNotifier.SendWebSocketDisconnected(e.LastSeenHeight, nil)
		}
	}

	// 12. Create subscriber.
	sub := subscriber.NewSubscriber(
		source,
		logger,
		cfg.CometBFT.ReconnectBaseDelay,
		cfg.CometBFT.ReconnectMaxDelay,
		cfg.CometBFT.HeartbeatTimeout,
		stateChangeCB,
	)

	// 13. Create Backfiller (uses its own RPC client, not the subscriber's).
	initialFrom, _ := cmd.Flags().GetString("from")
	backfiller := NewBackfiller(
		cfg.CometBFT.RPCURL,
		proc,
		reporter,
		db,
		m,
		opsNotifier,
		logger,
		cfg.Backfill.Delay,
		cfg.Backfill.ProgressInterval,
		initialFrom,
		cfg.Backfill.LiveCatchupThreshold,
	)

	// 14. Wire gap callback for reconnect-triggered backfill.
	sub.SetOnGap(func(fromHeight, toHeight int64) {
		go backfiller.BackfillRange(fromHeight, toHeight)
	})

	// 15. Wire per-block callback for height tracking and block rate metrics.
	// Both gauges update on every block so processing lag stays accurate.
	sub.SetOnBlock(func(height int64) {
		m.SetBlockHeight(float64(height))
		m.SetLastProcessedHeight(float64(height))
		m.IncrementBlocksProcessed(true)
	})

	// 16. Create Monitor and run.
	mon := NewMonitor(
		sub, proc, db, metricsServer, m, notif, reporter,
		source, opsNotifier, backfiller, logger,
	)

	log.Info().Msg("all components initialized, starting monitor")
	success = true // Prevent deferred cleanup -- Monitor.shutdown() owns it now.
	return mon.Run(ctx)
}
