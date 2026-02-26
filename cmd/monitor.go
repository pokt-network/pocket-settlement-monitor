package cmd

import (
	"context"
	"errors"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/notify"
	"github.com/pokt-network/pocket-settlement-monitor/processor"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

const defaultShutdownTimeout = 15 * time.Second

// Monitor orchestrates all components of the settlement monitoring pipeline:
// subscriber, collector, processor, store, metrics, reporter, notifier, and backfiller.
// It uses errgroup for concurrent lifecycle management and signal.NotifyContext
// for graceful SIGINT/SIGTERM handling.
type Monitor struct {
	// Components
	subscriber    *subscriber.Subscriber
	processor     *processor.Processor
	store         store.Store
	metricsServer *metrics.Server
	metrics       *metrics.Metrics
	notifier      *notify.Notifier
	reporter      *processor.Reporter
	source        *subscriber.CometBFTSource
	opsNotifier   *OpsNotifier
	backfiller    *Backfiller
	logger        zerolog.Logger

	// Session stats
	startTime       time.Time
	blocksProcessed atomic.Int64
	eventsProcessed atomic.Int64

	// Shutdown
	shutdownTimeout time.Duration
}

// NewMonitor creates a Monitor with all components wired. The collector is created
// internally with a block-counting wrapper around the processor.
func NewMonitor(
	sub *subscriber.Subscriber,
	proc *processor.Processor,
	st store.Store,
	metricsSrv *metrics.Server,
	m *metrics.Metrics,
	notif *notify.Notifier,
	rep *processor.Reporter,
	src *subscriber.CometBFTSource,
	ops *OpsNotifier,
	bf *Backfiller,
	logger zerolog.Logger,
) *Monitor {
	return &Monitor{
		subscriber:      sub,
		processor:       proc,
		store:           st,
		metricsServer:   metricsSrv,
		metrics:         m,
		notifier:        notif,
		reporter:        rep,
		source:          src,
		opsNotifier:     ops,
		backfiller:      bf,
		logger:          logging.ForComponent(logger, "monitor"),
		shutdownTimeout: defaultShutdownTimeout,
	}
}

// Run starts the full monitoring pipeline and blocks until a signal is received
// or an unrecoverable error occurs. It starts all goroutines in an errgroup
// and handles ordered shutdown on completion.
func (m *Monitor) Run(ctx context.Context) error {
	m.startTime = time.Now()

	// Wrap context with signal handler for SIGINT, SIGTERM.
	// signal.NotifyContext only catches the first signal, which is correct:
	// per locked decision, "Second SIGINT does NOT force-exit".
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	g, gCtx := errgroup.WithContext(ctx)

	// 1. Start metrics server in errgroup.
	g.Go(func() error { return m.metricsServer.Start(gCtx) })

	// 2. Start notifier sender goroutine.
	m.notifier.Start(gCtx)

	// 3. Send ops notification: monitor started.
	m.opsNotifier.SendMonitorStarted()

	// 4. Start backfiller in errgroup.
	g.Go(func() error { return m.backfiller.RunIfNeeded(gCtx) })

	// 5. Start subscriber in errgroup.
	g.Go(func() error { return m.subscriber.Run(gCtx) })

	// 6. Start collector (with block-counting wrapper) in errgroup.
	g.Go(func() error { return m.runCollector(gCtx) })

	// 7. Wait for all goroutines to finish.
	err := g.Wait()

	// 8. Ordered shutdown of remaining components.
	m.shutdown()

	// Mask context.Canceled -- it's the expected signal-driven shutdown path.
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// runCollector creates a Collector with a block-counting wrapper around the
// processor and runs it. The wrapper intercepts block processing to:
// - Count blocks/events for session stats
// - Log readiness message after first block
// - Flip /ready endpoint to healthy
func (m *Monitor) runCollector(ctx context.Context) error {
	wrapper := &blockCountingProcessor{inner: m.processor, monitor: m}
	coll := processor.NewCollector(wrapper, m.logger)
	return coll.Run(ctx, m.subscriber.Events())
}

// shutdown performs ordered cleanup of components after the errgroup completes.
// Per locked decision, shutdown order is:
//  1. Context already canceled (subscriber stopped, collector flushed via errgroup.Wait())
//  2. Stop notifier (drains channel with 5s timeout)
//  3. Close SQLite
//  4. Stop metrics server with timeout
//  5. Log session summary
//
// Each step logs at Info level on success, Error level on failure.
// Failures do NOT abort the sequence -- all steps execute regardless.
func (m *Monitor) shutdown() {
	m.logger.Info().Msg("starting ordered shutdown")

	// 1. Stop notifier (drains pending notifications with 5s timeout).
	m.notifier.Stop()
	m.logger.Info().Msg("notifier stopped")

	// 2. Close SQLite.
	if err := m.store.Close(); err != nil {
		m.logger.Error().Err(err).Msg("error closing SQLite store")
	} else {
		m.logger.Info().Msg("SQLite store closed")
	}

	// 3. Stop metrics server with timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), m.shutdownTimeout)
	defer cancel()
	if err := m.metricsServer.Stop(shutdownCtx); err != nil {
		m.logger.Error().Err(err).Msg("error stopping metrics server")
	} else {
		m.logger.Info().Msg("metrics server stopped")
	}

	// 4. Log session summary.
	m.logger.Info().
		Dur("uptime", time.Since(m.startTime)).
		Int64("blocks_processed", m.blocksProcessed.Load()).
		Int64("events_processed", m.eventsProcessed.Load()).
		Msg("monitor stopped")
}

// blockCountingProcessor wraps a BlockProcessor to count processed blocks/events
// and trigger readiness on the first successfully processed block.
type blockCountingProcessor struct {
	inner          processor.BlockProcessor
	monitor        *Monitor
	firstBlockOnce sync.Once
}

func (w *blockCountingProcessor) ProcessBlock(ctx context.Context, height int64, ts time.Time, events []subscriber.SettlementEvent, isLive bool) error {
	err := w.inner.ProcessBlock(ctx, height, ts, events, isLive)
	if err == nil {
		w.monitor.blocksProcessed.Add(1)
		w.monitor.eventsProcessed.Add(int64(len(events)))
		w.firstBlockOnce.Do(func() {
			w.monitor.metricsServer.SetWSConnected(true) // flip /ready
			w.monitor.logger.Info().
				Int64("height", height).
				Msg("monitor ready -- processing live blocks")
		})
	}
	return err
}
