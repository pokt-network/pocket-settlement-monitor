package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

const metricsNamespace = "psm"

// Metrics holds all Prometheus metric references for the settlement monitor.
type Metrics struct {
	Labels *LabelConfig

	// Event counters
	ClaimsSettled            *prometheus.CounterVec
	ClaimsExpired            *prometheus.CounterVec
	SuppliersSlashed         *prometheus.CounterVec
	ClaimsDiscarded          *prometheus.CounterVec
	ApplicationsOverserviced *prometheus.CounterVec

	// Revenue counters
	UpoktEarned       *prometheus.CounterVec
	UpoktClaimed      *prometheus.CounterVec
	UpoktLostExpired  *prometheus.CounterVec
	UpoktOverserviced prometheus.Counter

	// Relay/CU counters
	RelaysSettled                *prometheus.CounterVec
	EstimatedRelaysSettled       *prometheus.CounterVec
	RelaysExpired                *prometheus.CounterVec
	EstimatedRelaysExpired       *prometheus.CounterVec
	ComputeUnitsSettled          *prometheus.CounterVec
	EstimatedComputeUnitsSettled *prometheus.CounterVec

	// Settlement latency histogram (event_type label only)
	SettlementLatencyBlocks *prometheus.HistogramVec

	// Operational gauges
	CurrentBlockHeight      prometheus.Gauge
	LastProcessedHeight     prometheus.Gauge
	BackfillBlocksRemaining prometheus.Gauge
	WebsocketConnected      prometheus.Gauge
	SuppliersMonitored      prometheus.Gauge
	Info                    *prometheus.GaugeVec

	// Operational counters
	BlocksProcessed     prometheus.Counter
	GapDetected         prometheus.Counter
	WebsocketReconnects prometheus.Counter

	// Notification counters
	DiscordNotificationsSent  *prometheus.CounterVec
	DiscordNotificationErrors *prometheus.CounterVec

	// Store counters
	SQLiteOperations     *prometheus.CounterVec
	RetentionRowsDeleted *prometheus.CounterVec
}

// eventLabels are the standard labels used across event, revenue, and relay counters.
var eventLabels = []string{"event_type", "supplier", "service", "application"}

// NewMetrics creates and registers all Prometheus metrics on the given registry.
func NewMetrics(registry *prometheus.Registry, labels *LabelConfig) *Metrics {
	// Register standard Go and process collectors.
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	factory := promauto.With(registry)

	m := &Metrics{
		Labels: labels,

		// Event counters
		ClaimsSettled: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "claims_settled_total",
			Help:      "Total claims settled.",
		}, eventLabels),
		ClaimsExpired: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "claims_expired_total",
			Help:      "Total claims expired.",
		}, eventLabels),
		SuppliersSlashed: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "suppliers_slashed_total",
			Help:      "Total supplier slashes.",
		}, eventLabels),
		ClaimsDiscarded: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "claims_discarded_total",
			Help:      "Total claims discarded.",
		}, eventLabels),
		ApplicationsOverserviced: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "applications_overserviced_total",
			Help:      "Total overservice events.",
		}, eventLabels),

		// Revenue counters
		UpoktEarned: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "upokt_earned_total",
			Help:      "Total uPOKT earned from settlements.",
		}, eventLabels),
		UpoktClaimed: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "upokt_claimed_total",
			Help:      "Total uPOKT claimed.",
		}, eventLabels),
		UpoktLostExpired: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "upokt_lost_expired_total",
			Help:      "Total uPOKT lost to expirations.",
		}, eventLabels),
		UpoktOverserviced: factory.NewCounter(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "upokt_overserviced_total",
			Help:      "Total uPOKT lost to overservice (expected burn - effective burn).",
		}),

		// Relay/CU counters
		RelaysSettled: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "relays_settled_total",
			Help:      "Merkle tree relays settled (passed difficulty).",
		}, eventLabels),
		EstimatedRelaysSettled: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "estimated_relays_settled_total",
			Help:      "Expanded relay count settled (real throughput).",
		}, eventLabels),
		RelaysExpired: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "relays_expired_total",
			Help:      "Merkle tree relays lost.",
		}, eventLabels),
		EstimatedRelaysExpired: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "estimated_relays_expired_total",
			Help:      "Expanded relays lost.",
		}, eventLabels),
		ComputeUnitsSettled: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "compute_units_settled_total",
			Help:      "CUs from merkle tree settled.",
		}, eventLabels),
		EstimatedComputeUnitsSettled: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "estimated_compute_units_settled_total",
			Help:      "Estimated CUs settled.",
		}, eventLabels),

		// Settlement latency histogram (event_type label only per locked decision)
		SettlementLatencyBlocks: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Name:      "settlement_latency_blocks",
			Help:      "Blocks between session end and settlement.",
			Buckets:   []float64{10, 20, 30, 50, 75, 100, 150, 200, 300},
		}, []string{"event_type"}),

		// Operational gauges
		CurrentBlockHeight: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "current_block_height",
			Help:      "Latest height from WebSocket.",
		}),
		LastProcessedHeight: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "last_processed_height",
			Help:      "Latest height in SQLite.",
		}),
		BackfillBlocksRemaining: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "backfill_blocks_remaining",
			Help:      "Blocks left to backfill (0 = caught up).",
		}),
		WebsocketConnected: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "websocket_connected",
			Help:      "1 if connected, 0 if disconnected.",
		}),
		SuppliersMonitored: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "suppliers_monitored",
			Help:      "Number of supplier addresses being monitored.",
		}),
		Info: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "info",
			Help:      "Set to 1, carries build info.",
		}, []string{"version", "commit"}),

		// Operational counters
		BlocksProcessed: factory.NewCounter(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "blocks_processed_total",
			Help:      "Total blocks processed.",
		}),
		GapDetected: factory.NewCounter(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "gap_detected_total",
			Help:      "Number of gap detection events.",
		}),
		WebsocketReconnects: factory.NewCounter(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "websocket_reconnects_total",
			Help:      "Total WebSocket reconnection attempts.",
		}),

		// Notification counters
		DiscordNotificationsSent: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "discord_notifications_sent_total",
			Help:      "Successful Discord webhook sends.",
		}, []string{"type"}),
		DiscordNotificationErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "discord_notification_errors_total",
			Help:      "Failed Discord webhook sends.",
		}, []string{"type"}),

		// Store counters
		SQLiteOperations: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "sqlite_operations_total",
			Help:      "SQLite operation counts.",
		}, []string{"operation", "status"}),
		RetentionRowsDeleted: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "retention_rows_deleted_total",
			Help:      "Rows deleted by retention cleanup.",
		}, []string{"table"}),
	}

	return m
}

// --- Recording methods ---
// All counter methods accept an isLive parameter. Counters ONLY increment when isLive == true.
// Gauges always update regardless of isLive.

// RecordClaimSettled increments the appropriate event counter for a settlement event.
func (m *Metrics) RecordClaimSettled(eventType, supplier, service, application string, isLive bool) {
	if !isLive {
		return
	}
	m.ClaimsSettled.With(m.Labels.Labels(eventType, supplier, service, application)).Inc()
}

// RecordClaimExpired increments the claim expired counter.
func (m *Metrics) RecordClaimExpired(eventType, supplier, service, application string, isLive bool) {
	if !isLive {
		return
	}
	m.ClaimsExpired.With(m.Labels.Labels(eventType, supplier, service, application)).Inc()
}

// RecordSupplierSlashed increments the supplier slashed counter.
func (m *Metrics) RecordSupplierSlashed(eventType, supplier, service, application string, isLive bool) {
	if !isLive {
		return
	}
	m.SuppliersSlashed.With(m.Labels.Labels(eventType, supplier, service, application)).Inc()
}

// RecordClaimDiscarded increments the claim discarded counter.
func (m *Metrics) RecordClaimDiscarded(eventType, supplier, service, application string, isLive bool) {
	if !isLive {
		return
	}
	m.ClaimsDiscarded.With(m.Labels.Labels(eventType, supplier, service, application)).Inc()
}

// RecordApplicationOverserviced increments the overservice counter.
func (m *Metrics) RecordApplicationOverserviced(eventType, supplier, service, application string, isLive bool) {
	if !isLive {
		return
	}
	m.ApplicationsOverserviced.With(m.Labels.Labels(eventType, supplier, service, application)).Inc()
}

// RecordOverserviceAmount adds the overservice uPOKT amount (expected - effective burn).
func (m *Metrics) RecordOverserviceAmount(amount float64, isLive bool) {
	if !isLive {
		return
	}
	m.UpoktOverserviced.Add(amount)
}

// RecordRevenue adds the given amount to the specified revenue counter.
func (m *Metrics) RecordRevenue(counter *prometheus.CounterVec, eventType, supplier, service, application string, amount float64, isLive bool) {
	if !isLive {
		return
	}
	counter.With(m.Labels.Labels(eventType, supplier, service, application)).Add(amount)
}

// RecordRelays adds the given count to the specified relay/CU counter.
func (m *Metrics) RecordRelays(counter *prometheus.CounterVec, eventType, supplier, service, application string, count float64, isLive bool) {
	if !isLive {
		return
	}
	counter.With(m.Labels.Labels(eventType, supplier, service, application)).Add(count)
}

// ObserveLatency records a settlement latency observation.
// Only recorded for live events.
func (m *Metrics) ObserveLatency(eventType string, blocks float64, isLive bool) {
	if !isLive {
		return
	}
	m.SettlementLatencyBlocks.With(prometheus.Labels{"event_type": eventType}).Observe(blocks)
}

// SetBlockHeight updates the current block height gauge. Always updates regardless of isLive.
func (m *Metrics) SetBlockHeight(height float64) {
	m.CurrentBlockHeight.Set(height)
}

// SetLastProcessedHeight updates the last processed height gauge. Always updates.
func (m *Metrics) SetLastProcessedHeight(height float64) {
	m.LastProcessedHeight.Set(height)
}

// SetBackfillBlocksRemaining updates the backfill blocks remaining gauge. Always updates.
func (m *Metrics) SetBackfillBlocksRemaining(remaining float64) {
	m.BackfillBlocksRemaining.Set(remaining)
}

// SetWebsocketConnected updates the websocket_connected Prometheus gauge.
// This is separate from Server.SetWSConnected which updates the /ready endpoint.
func (m *Metrics) SetWebsocketConnected(connected bool) {
	if connected {
		m.WebsocketConnected.Set(1.0)
	} else {
		m.WebsocketConnected.Set(0.0)
	}
}

// SetSuppliersMonitored updates the suppliers_monitored gauge. Always updates.
func (m *Metrics) SetSuppliersMonitored(count float64) {
	m.SuppliersMonitored.Set(count)
}

// SetInfo sets the info gauge with version and commit labels.
func (m *Metrics) SetInfo(version, commit string) {
	m.Info.With(prometheus.Labels{"version": version, "commit": commit}).Set(1)
}

// IncrementBlocksProcessed increments the blocks processed counter.
func (m *Metrics) IncrementBlocksProcessed(isLive bool) {
	if !isLive {
		return
	}
	m.BlocksProcessed.Inc()
}

// IncrementGapDetected increments the gap detected counter.
func (m *Metrics) IncrementGapDetected(isLive bool) {
	if !isLive {
		return
	}
	m.GapDetected.Inc()
}

// RecordDiscordNotification increments the discord notification sent counter.
func (m *Metrics) RecordDiscordNotification(notificationType string, isLive bool) {
	if !isLive {
		return
	}
	m.DiscordNotificationsSent.With(prometheus.Labels{"type": notificationType}).Inc()
}

// RecordDiscordNotificationError increments the discord notification error counter.
func (m *Metrics) RecordDiscordNotificationError(notificationType string, isLive bool) {
	if !isLive {
		return
	}
	m.DiscordNotificationErrors.With(prometheus.Labels{"type": notificationType}).Inc()
}

// RecordSQLiteOperation increments the SQLite operation counter.
func (m *Metrics) RecordSQLiteOperation(operation, status string, isLive bool) {
	if !isLive {
		return
	}
	m.SQLiteOperations.With(prometheus.Labels{"operation": operation, "status": status}).Inc()
}

// RecordRetentionRowsDeleted adds to the retention rows deleted counter.
func (m *Metrics) RecordRetentionRowsDeleted(table string, count float64, isLive bool) {
	if !isLive {
		return
	}
	m.RetentionRowsDeleted.With(prometheus.Labels{"table": table}).Add(count)
}

// NewStateChangeCallback creates a subscriber.StateChangeCallback that wires
// subscriber connection state changes to both the Prometheus gauge and the
// HTTP server's /ready endpoint.
func NewStateChangeCallback(m *Metrics, s *Server) subscriber.StateChangeCallback {
	return func(event subscriber.StateChangeEvent) {
		switch event.State {
		case subscriber.StateConnected:
			m.SetWebsocketConnected(true)
			s.SetWSConnected(true)
		case subscriber.StateReconnected:
			m.SetWebsocketConnected(true)
			s.SetWSConnected(true)
			m.WebsocketReconnects.Inc()
		case subscriber.StateDisconnected:
			m.SetWebsocketConnected(false)
			s.SetWSConnected(false)
		}
	}
}
