package processor

import (
	"context"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/rs/zerolog"

	tokenomicstypes "github.com/pokt-network/poktroll/x/tokenomics/types"

	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/store"
	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// BlockReporter reports processed block data (e.g., periodic summary output).
// Phase 5 provides the real implementation; this package uses a no-op default.
type BlockReporter interface {
	ReportBlock(ctx context.Context, height int64, ts time.Time,
		settlements []store.Settlement,
		overservices []store.OverserviceEvent,
		reimbursements []store.ReimbursementEvent) error
}

// BlockNotifier sends notifications for notable block events (e.g., Discord webhook).
// Phase 6 provides the real implementation; this package uses a no-op default.
type BlockNotifier interface {
	NotifyBlock(ctx context.Context, height int64, settlements []store.Settlement, overservices []store.OverserviceEvent, reimbursements []store.ReimbursementEvent)
}

// noopReporter is the default BlockReporter that does nothing.
type noopReporter struct{}

func (noopReporter) ReportBlock(_ context.Context, _ int64, _ time.Time, _ []store.Settlement, _ []store.OverserviceEvent, _ []store.ReimbursementEvent) error {
	return nil
}

// noopNotifier is the default BlockNotifier that does nothing.
type noopNotifier struct{}

func (noopNotifier) NotifyBlock(_ context.Context, _ int64, _ []store.Settlement, _ []store.OverserviceEvent, _ []store.ReimbursementEvent) {
}

// Processor receives a flushed block from the Collector, filters events by supplier,
// converts proto messages to store models, correlates overservice, and dispatches
// to store, metrics, reporter, and notifier.
type Processor struct {
	store    store.Store
	metrics  *metrics.Metrics
	filter   *SupplierFilter
	reporter BlockReporter
	notifier BlockNotifier
	logger   zerolog.Logger
}

// NewProcessor creates a Processor with no-op reporter and notifier defaults.
func NewProcessor(s store.Store, m *metrics.Metrics, filter *SupplierFilter, logger zerolog.Logger) *Processor {
	return &Processor{
		store:    s,
		metrics:  m,
		filter:   filter,
		reporter: noopReporter{},
		notifier: noopNotifier{},
		logger:   logging.ForComponent(logger, "processor"),
	}
}

// SetReporter injects a real BlockReporter for Phase 5 integration.
func (p *Processor) SetReporter(r BlockReporter) {
	p.reporter = r
}

// SetNotifier injects a real BlockNotifier for Phase 6 integration.
func (p *Processor) SetNotifier(n BlockNotifier) {
	p.notifier = n
}

// ProcessBlock processes all events for a single block through the full pipeline:
// filter -> convert -> correlate -> store -> metrics -> reporter -> notifier -> log.
func (p *Processor) ProcessBlock(ctx context.Context, height int64, ts time.Time, events []subscriber.SettlementEvent, isLive bool) error {

	// 1. Filter: keep only events from monitored suppliers.
	var filtered []subscriber.SettlementEvent
	for _, e := range events {
		addr := extractSupplierAddress(e.Event)
		if p.filter.Match(addr) {
			filtered = append(filtered, e)
		}
	}

	// 2. Convert: type-switch on each filtered event, collect into store models.
	var settlements []store.Settlement
	var overservices []store.OverserviceEvent
	var reimbursements []store.ReimbursementEvent
	rewardDists := make(map[int][]store.RewardDistribution)
	summary := BlockSummary{TotalEvents: len(filtered)}

	for _, e := range filtered {
		switch msg := e.Event.(type) {
		case *tokenomicstypes.EventClaimSettled:
			settlement, rewards := convertSettledEventWithRewards(msg, height, ts)
			rewardDists[len(settlements)] = rewards
			settlements = append(settlements, settlement)
			summary.SettledUpokt += settlement.ClaimedUpokt
			summary.SettledRelays += settlement.NumRelays
			summary.SettledEstRelays += settlement.EstimatedRelays
			summary.SettledComputeUnits += settlement.NumClaimedComputeUnits
			summary.SettledEstComputeUnits += settlement.NumEstimatedComputeUnits

		case *tokenomicstypes.EventClaimExpired:
			settlement := convertExpiredEvent(msg, height, ts)
			settlements = append(settlements, settlement)
			summary.ExpiredUpokt += settlement.ClaimedUpokt
			summary.ExpiredRelays += settlement.NumRelays
			summary.ExpiredEstRelays += settlement.EstimatedRelays
			summary.ExpiredComputeUnits += settlement.NumClaimedComputeUnits
			summary.ExpiredEstComputeUnits += settlement.NumEstimatedComputeUnits

		case *tokenomicstypes.EventSupplierSlashed:
			settlement := convertSlashedEvent(msg, height, ts)
			settlements = append(settlements, settlement)
			summary.SlashedUpokt += settlement.SlashPenaltyUpokt

		case *tokenomicstypes.EventClaimDiscarded:
			settlement := convertDiscardedEvent(msg, height, ts)
			settlements = append(settlements, settlement)

		case *tokenomicstypes.EventApplicationOverserviced:
			osEvent := convertOverserviceEvent(msg, height, ts)
			overservices = append(overservices, osEvent)
			summary.OverserviceUpokt += osEvent.ExpectedBurnUpokt - osEvent.EffectiveBurnUpokt

		case *tokenomicstypes.EventApplicationReimbursementRequest:
			rEvent := convertReimbursementEvent(msg, height, ts)
			reimbursements = append(reimbursements, rEvent)
		}
	}

	// 3. Correlate: mark overserviced settlements in the same block.
	correlations := correlateOverservice(events, settlements)
	summary.OverserviceCorrelations = correlations

	// 4. Build processed block record.
	source := "live"
	if !isLive {
		source = "backfill"
	}
	block := store.ProcessedBlock{
		Height:         height,
		BlockTimestamp: ts,
		EventCount:     len(filtered),
		Source:         source,
	}

	// 5. Dispatch to store (always, regardless of isLive).
	if err := p.store.InsertBlockEvents(ctx, block, settlements, rewardDists, overservices, reimbursements); err != nil {
		return err
	}

	// 6. Dispatch to metrics (isLive guard).
	p.dispatchMetrics(settlements, overservices, reimbursements, height, isLive)

	// 7. Dispatch to reporter (always).
	if err := p.reporter.ReportBlock(ctx, height, ts, settlements, overservices, reimbursements); err != nil {
		p.logger.Error().Err(err).Int64("height", height).Msg("reporter error")
	}

	// 8. Dispatch to notifier (async, live only per NOTF-05).
	if isLive {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error().Interface("panic", r).Int64("height", height).Msg("panic in NotifyBlock")
				}
			}()
			p.notifier.NotifyBlock(ctx, height, settlements, overservices, reimbursements)
		}()
	}

	// 9. Log block summary.
	p.logger.Info().
		Int64("height", height).
		Int("events", summary.TotalEvents).
		Int("overservice_correlated", summary.OverserviceCorrelations).
		Int64("settled_upokt", summary.SettledUpokt).
		Int64("expired_upokt", summary.ExpiredUpokt).
		Int64("slashed_upokt", summary.SlashedUpokt).
		Int64("overservice_upokt", summary.OverserviceUpokt).
		Int64("settled_relays", summary.SettledRelays).
		Int64("expired_relays", summary.ExpiredRelays).
		Int64("settled_est_relays", summary.SettledEstRelays).
		Int64("expired_est_relays", summary.ExpiredEstRelays).
		Int64("settled_compute_units", summary.SettledComputeUnits).
		Int64("expired_compute_units", summary.ExpiredComputeUnits).
		Int64("settled_est_compute_units", summary.SettledEstComputeUnits).
		Int64("expired_est_compute_units", summary.ExpiredEstComputeUnits).
		Msg("block processed")

	return nil
}

// dispatchMetrics sends event data to Prometheus counters and gauges.
// Counter methods are gated by isLive (backfill events never increment counters).
// Gauge methods always update regardless of isLive.
func (p *Processor) dispatchMetrics(settlements []store.Settlement, overservices []store.OverserviceEvent, reimbursements []store.ReimbursementEvent, height int64, isLive bool) {
	// Always update gauges.
	p.metrics.SetLastProcessedHeight(float64(height))

	// Counter increments gated by isLive.
	for _, s := range settlements {
		supplier := s.SupplierOperatorAddress
		service := s.ServiceID
		app := s.ApplicationAddress

		switch s.EventType {
		case "settled":
			p.metrics.RecordClaimSettled(s.EventType, supplier, service, app, isLive)
			p.metrics.RecordRevenue(p.metrics.UpoktEarned, s.EventType, supplier, service, app, float64(s.ClaimedUpokt), isLive)
			p.metrics.RecordRelays(p.metrics.RelaysSettled, s.EventType, supplier, service, app, float64(s.NumRelays), isLive)
			p.metrics.RecordRelays(p.metrics.EstimatedRelaysSettled, s.EventType, supplier, service, app, float64(s.EstimatedRelays), isLive)
			p.metrics.RecordRelays(p.metrics.ComputeUnitsSettled, s.EventType, supplier, service, app, float64(s.NumClaimedComputeUnits), isLive)
			p.metrics.RecordRelays(p.metrics.EstimatedComputeUnitsSettled, s.EventType, supplier, service, app, float64(s.NumEstimatedComputeUnits), isLive)
			if s.SessionEndBlockHeight > 0 {
				latency := float64(height - s.SessionEndBlockHeight)
				p.metrics.ObserveLatency(s.EventType, latency, isLive)
			}
		case "expired":
			p.metrics.RecordClaimExpired(s.EventType, supplier, service, app, isLive)
			p.metrics.RecordRevenue(p.metrics.UpoktLostExpired, s.EventType, supplier, service, app, float64(s.ClaimedUpokt), isLive)
			p.metrics.RecordRelays(p.metrics.RelaysExpired, s.EventType, supplier, service, app, float64(s.NumRelays), isLive)
			p.metrics.RecordRelays(p.metrics.EstimatedRelaysExpired, s.EventType, supplier, service, app, float64(s.EstimatedRelays), isLive)
			if s.SessionEndBlockHeight > 0 {
				latency := float64(height - s.SessionEndBlockHeight)
				p.metrics.ObserveLatency(s.EventType, latency, isLive)
			}
		case "slashed":
			p.metrics.RecordSupplierSlashed(s.EventType, supplier, service, app, isLive)
			p.metrics.RecordRevenue(p.metrics.UpoktClaimed, s.EventType, supplier, service, app, float64(s.SlashPenaltyUpokt), isLive)
		case "discarded":
			p.metrics.RecordClaimDiscarded(s.EventType, supplier, service, app, isLive)
		}
	}

	for _, o := range overservices {
		p.metrics.RecordApplicationOverserviced("overserviced", o.SupplierOperatorAddress, "", o.ApplicationAddress, isLive)
		overserviceAmount := float64(o.ExpectedBurnUpokt - o.EffectiveBurnUpokt)
		if overserviceAmount > 0 {
			p.metrics.RecordOverserviceAmount(overserviceAmount, isLive)
		}
	}

	_ = reimbursements // Reimbursement metrics can be added later if needed.

	// Note: blocks_processed_total is incremented by the subscriber's OnBlock callback
	// for every block (not just settlement blocks), so we don't increment it here.
}

// extractSupplierAddress extracts the supplier operator address from a proto message.
// Handles the field name inconsistency: SupplierOperatorAddr for overservice/reimbursement,
// SupplierOperatorAddress for the other 4 event types.
func extractSupplierAddress(event proto.Message) string {
	switch msg := event.(type) {
	case *tokenomicstypes.EventClaimSettled:
		return msg.SupplierOperatorAddress
	case *tokenomicstypes.EventClaimExpired:
		return msg.SupplierOperatorAddress
	case *tokenomicstypes.EventSupplierSlashed:
		return msg.SupplierOperatorAddress
	case *tokenomicstypes.EventClaimDiscarded:
		return msg.SupplierOperatorAddress
	case *tokenomicstypes.EventApplicationOverserviced:
		return msg.SupplierOperatorAddr
	case *tokenomicstypes.EventApplicationReimbursementRequest:
		return msg.SupplierOperatorAddr
	default:
		return ""
	}
}
