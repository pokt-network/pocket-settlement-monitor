package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pokt-network/pocket-settlement-monitor/subscriber"
)

// newTestMetrics creates a fresh registry and Metrics for test isolation.
func newTestMetrics(labels *LabelConfig) (*Metrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	if labels == nil {
		labels = &LabelConfig{}
	}
	m := NewMetrics(reg, labels)
	return m, reg
}

// getCounterValue extracts the current value of a counter metric from the registry.
func getCounterValue(t *testing.T, reg *prometheus.Registry, name string, lbls prometheus.Labels) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)

	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if matchLabels(m.GetLabel(), lbls) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// getGaugeValue extracts the current value of a gauge metric from the registry.
func getGaugeValue(t *testing.T, reg *prometheus.Registry, name string, lbls prometheus.Labels) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)

	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if matchLabels(m.GetLabel(), lbls) {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}

// matchLabels checks whether a metric's label pairs match the expected labels.
func matchLabels(pairs []*dto.LabelPair, expected prometheus.Labels) bool {
	if len(expected) == 0 && len(pairs) == 0 {
		return true
	}
	for k, v := range expected {
		found := false
		for _, lp := range pairs {
			if lp.GetName() == k && lp.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestMetrics_AllRegistered(t *testing.T) {
	_, reg := newTestMetrics(nil)

	families, err := reg.Gather()
	require.NoError(t, err)

	// Build a set of metric family names.
	names := make(map[string]bool)
	for _, mf := range families {
		names[mf.GetName()] = true
	}

	// Verify key metrics are present (Go/process collectors + our psm_ metrics).
	// Go/process collectors are automatically registered, so we check a few psm_ ones.
	expectedPSM := []string{
		"psm_claims_settled_total",
		"psm_claims_expired_total",
		"psm_suppliers_slashed_total",
		"psm_claims_discarded_total",
		"psm_applications_overserviced_total",
		"psm_upokt_earned_total",
		"psm_upokt_claimed_total",
		"psm_upokt_lost_expired_total",
		"psm_relays_settled_total",
		"psm_estimated_relays_settled_total",
		"psm_relays_expired_total",
		"psm_estimated_relays_expired_total",
		"psm_compute_units_settled_total",
		"psm_estimated_compute_units_settled_total",
		"psm_settlement_latency_blocks",
		"psm_current_block_height",
		"psm_last_processed_height",
		"psm_backfill_blocks_remaining",
		"psm_websocket_connected",
		"psm_suppliers_monitored",
		"psm_blocks_processed_total",
		"psm_gap_detected_total",
		"psm_websocket_reconnects_total",
		"psm_discord_notifications_sent_total",
		"psm_discord_notification_errors_total",
		"psm_sqlite_operations_total",
		"psm_retention_rows_deleted_total",
	}

	// CounterVec and GaugeVec only appear in Gather output when they have observations.
	// Initialize some metrics so they appear.
	m, reg2 := newTestMetrics(nil)
	lbls := m.Labels.Labels("settled", "", "", "")

	m.ClaimsSettled.With(lbls).Inc()
	m.ClaimsExpired.With(lbls).Inc()
	m.SuppliersSlashed.With(lbls).Inc()
	m.ClaimsDiscarded.With(lbls).Inc()
	m.ApplicationsOverserviced.With(lbls).Inc()
	m.UpoktEarned.With(lbls).Inc()
	m.UpoktClaimed.With(lbls).Inc()
	m.UpoktLostExpired.With(lbls).Inc()
	m.RelaysSettled.With(lbls).Inc()
	m.EstimatedRelaysSettled.With(lbls).Inc()
	m.RelaysExpired.With(lbls).Inc()
	m.EstimatedRelaysExpired.With(lbls).Inc()
	m.ComputeUnitsSettled.With(lbls).Inc()
	m.EstimatedComputeUnitsSettled.With(lbls).Inc()
	m.SettlementLatencyBlocks.With(prometheus.Labels{"event_type": "settled"}).Observe(10)
	m.SetBlockHeight(100)
	m.SetLastProcessedHeight(99)
	m.SetBackfillBlocksRemaining(0)
	m.SetWebsocketConnected(true)
	m.SetSuppliersMonitored(5)
	m.SetInfo("dev", "none")
	m.BlocksProcessed.Inc()
	m.GapDetected.Inc()
	m.WebsocketReconnects.Inc()
	m.DiscordNotificationsSent.With(prometheus.Labels{"type": "settlement"}).Inc()
	m.DiscordNotificationErrors.With(prometheus.Labels{"type": "settlement"}).Inc()
	m.SQLiteOperations.With(prometheus.Labels{"operation": "insert", "status": "ok"}).Inc()
	m.RetentionRowsDeleted.With(prometheus.Labels{"table": "settlements"}).Inc()

	families2, err := reg2.Gather()
	require.NoError(t, err)

	names2 := make(map[string]bool)
	for _, mf := range families2 {
		names2[mf.GetName()] = true
	}

	for _, expected := range expectedPSM {
		assert.True(t, names2[expected], "expected metric %s to be registered", expected)
	}
}

func TestMetrics_IsLiveGuard_ClaimSettled(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"event_type": "settled", "supplier": "", "service": "", "application": ""}

	// isLive=false should NOT increment.
	m.RecordClaimSettled("settled", "addr1", "svc1", "app1", false)
	val := getCounterValue(t, reg, "psm_claims_settled_total", lbls)
	assert.Equal(t, float64(0), val, "counter should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordClaimSettled("settled", "addr1", "svc1", "app1", true)
	val = getCounterValue(t, reg, "psm_claims_settled_total", lbls)
	assert.Equal(t, float64(1), val, "counter should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_Revenue(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"event_type": "settled", "supplier": "", "service": "", "application": ""}

	// isLive=false should NOT increment.
	m.RecordRevenue(m.UpoktEarned, "settled", "addr1", "svc1", "app1", 1000, false)
	val := getCounterValue(t, reg, "psm_upokt_earned_total", lbls)
	assert.Equal(t, float64(0), val, "revenue counter should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordRevenue(m.UpoktEarned, "settled", "addr1", "svc1", "app1", 1000, true)
	val = getCounterValue(t, reg, "psm_upokt_earned_total", lbls)
	assert.Equal(t, float64(1000), val, "revenue counter should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_BlocksProcessed(t *testing.T) {
	m, reg := newTestMetrics(nil)

	// isLive=false should NOT increment.
	m.IncrementBlocksProcessed(false)
	val := getCounterValue(t, reg, "psm_blocks_processed_total", nil)
	assert.Equal(t, float64(0), val, "blocks processed should not increment when isLive=false")

	// isLive=true should increment.
	m.IncrementBlocksProcessed(true)
	val = getCounterValue(t, reg, "psm_blocks_processed_total", nil)
	assert.Equal(t, float64(1), val, "blocks processed should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_Relays(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"event_type": "settled", "supplier": "", "service": "", "application": ""}

	// isLive=false should NOT increment.
	m.RecordRelays(m.RelaysSettled, "settled", "addr1", "svc1", "app1", 50, false)
	val := getCounterValue(t, reg, "psm_relays_settled_total", lbls)
	assert.Equal(t, float64(0), val, "relay counter should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordRelays(m.RelaysSettled, "settled", "addr1", "svc1", "app1", 50, true)
	val = getCounterValue(t, reg, "psm_relays_settled_total", lbls)
	assert.Equal(t, float64(50), val, "relay counter should increment when isLive=true")
}

func TestMetrics_GaugesUpdateRegardlessOfIsLive(t *testing.T) {
	m, reg := newTestMetrics(nil)

	// SetBlockHeight always updates (no isLive param).
	m.SetBlockHeight(12345)
	val := getGaugeValue(t, reg, "psm_current_block_height", nil)
	assert.Equal(t, float64(12345), val)

	// SetWebsocketConnected always updates.
	m.SetWebsocketConnected(true)
	val = getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(1), val)

	m.SetWebsocketConnected(false)
	val = getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(0), val)
}

func TestMetrics_SetWebsocketConnected(t *testing.T) {
	m, reg := newTestMetrics(nil)

	m.SetWebsocketConnected(true)
	val := getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(1.0), val, "websocket_connected should be 1 when connected")

	m.SetWebsocketConnected(false)
	val = getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(0.0), val, "websocket_connected should be 0 when disconnected")
}

func TestLabelConfig_Enabled(t *testing.T) {
	lc := &LabelConfig{
		IncludeSupplier:    true,
		IncludeService:     true,
		IncludeApplication: true,
	}

	assert.Equal(t, "addr1", lc.SupplierLabel("addr1"))
	assert.Equal(t, "svc1", lc.ServiceLabel("svc1"))
	assert.Equal(t, "app1", lc.ApplicationLabel("app1"))
}

func TestLabelConfig_Disabled(t *testing.T) {
	lc := &LabelConfig{
		IncludeSupplier:    false,
		IncludeService:     false,
		IncludeApplication: false,
	}

	assert.Equal(t, "", lc.SupplierLabel("addr1"))
	assert.Equal(t, "", lc.ServiceLabel("svc1"))
	assert.Equal(t, "", lc.ApplicationLabel("app1"))
}

func TestLabelConfig_Labels(t *testing.T) {
	lc := &LabelConfig{
		IncludeSupplier: true,
		IncludeService:  false,
	}

	lbls := lc.Labels("settled", "addr1", "svc1", "app1")
	assert.Equal(t, "settled", lbls["event_type"])
	assert.Equal(t, "addr1", lbls["supplier"])
	assert.Equal(t, "", lbls["service"])
	assert.Equal(t, "", lbls["application"])
}

func TestMetrics_NewStateChangeCallback(t *testing.T) {
	m, reg := newTestMetrics(nil)
	srv := NewServer(":0", reg, testLogger())

	cb := NewStateChangeCallback(m, srv)

	// Connected -> gauge=1
	cb(subscriber.StateChangeEvent{State: subscriber.StateConnected})
	val := getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(1), val, "connected state should set gauge to 1")
	assert.True(t, srv.wsConnected.Load(), "server wsConnected should be true")

	// Disconnected -> gauge=0
	cb(subscriber.StateChangeEvent{State: subscriber.StateDisconnected})
	val = getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(0), val, "disconnected state should set gauge to 0")
	assert.False(t, srv.wsConnected.Load(), "server wsConnected should be false")

	// Reconnected -> gauge=1 + reconnects counter incremented
	cb(subscriber.StateChangeEvent{State: subscriber.StateReconnected})
	val = getGaugeValue(t, reg, "psm_websocket_connected", nil)
	assert.Equal(t, float64(1), val, "reconnected state should set gauge to 1")
	assert.True(t, srv.wsConnected.Load(), "server wsConnected should be true after reconnect")

	reconnects := getCounterValue(t, reg, "psm_websocket_reconnects_total", nil)
	assert.Equal(t, float64(1), reconnects, "reconnect should increment reconnects counter")
}
