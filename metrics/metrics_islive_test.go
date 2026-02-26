package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getHistogramSampleCount extracts the sample count of a histogram metric from the registry.
func getHistogramSampleCount(t *testing.T, reg *prometheus.Registry, name string, lbls prometheus.Labels) uint64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if matchLabels(m.GetLabel(), lbls) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func TestMetrics_IsLiveGuard_ObserveLatency(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"event_type": "settled"}

	// isLive=false should NOT record an observation.
	m.ObserveLatency("settled", 42, false)
	count := getHistogramSampleCount(t, reg, "psm_settlement_latency_blocks", lbls)
	assert.Equal(t, uint64(0), count, "histogram should have no samples when isLive=false")

	// isLive=true should record an observation.
	m.ObserveLatency("settled", 42, true)
	count = getHistogramSampleCount(t, reg, "psm_settlement_latency_blocks", lbls)
	assert.Equal(t, uint64(1), count, "histogram should have 1 sample when isLive=true")
}

func TestMetrics_IsLiveGuard_GapDetected(t *testing.T) {
	m, reg := newTestMetrics(nil)

	// isLive=false should NOT increment.
	m.IncrementGapDetected(false)
	val := getCounterValue(t, reg, "psm_gap_detected_total", nil)
	assert.Equal(t, float64(0), val, "gap detected should not increment when isLive=false")

	// isLive=true should increment.
	m.IncrementGapDetected(true)
	val = getCounterValue(t, reg, "psm_gap_detected_total", nil)
	assert.Equal(t, float64(1), val, "gap detected should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_DiscordNotification(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"type": "settlement"}

	// isLive=false should NOT increment.
	m.RecordDiscordNotification("settlement", false)
	val := getCounterValue(t, reg, "psm_discord_notifications_sent_total", lbls)
	assert.Equal(t, float64(0), val, "discord notifications should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordDiscordNotification("settlement", true)
	val = getCounterValue(t, reg, "psm_discord_notifications_sent_total", lbls)
	assert.Equal(t, float64(1), val, "discord notifications should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_DiscordNotificationError(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"type": "settlement"}

	// isLive=false should NOT increment.
	m.RecordDiscordNotificationError("settlement", false)
	val := getCounterValue(t, reg, "psm_discord_notification_errors_total", lbls)
	assert.Equal(t, float64(0), val, "discord notification errors should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordDiscordNotificationError("settlement", true)
	val = getCounterValue(t, reg, "psm_discord_notification_errors_total", lbls)
	assert.Equal(t, float64(1), val, "discord notification errors should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_SQLiteOperation(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"operation": "insert", "status": "ok"}

	// isLive=false should NOT increment.
	m.RecordSQLiteOperation("insert", "ok", false)
	val := getCounterValue(t, reg, "psm_sqlite_operations_total", lbls)
	assert.Equal(t, float64(0), val, "sqlite operations should not increment when isLive=false")

	// isLive=true should increment.
	m.RecordSQLiteOperation("insert", "ok", true)
	val = getCounterValue(t, reg, "psm_sqlite_operations_total", lbls)
	assert.Equal(t, float64(1), val, "sqlite operations should increment when isLive=true")
}

func TestMetrics_IsLiveGuard_RetentionRowsDeleted(t *testing.T) {
	m, reg := newTestMetrics(nil)
	lbls := prometheus.Labels{"table": "settlements"}

	// isLive=false should NOT add.
	m.RecordRetentionRowsDeleted("settlements", 5, false)
	val := getCounterValue(t, reg, "psm_retention_rows_deleted_total", lbls)
	assert.Equal(t, float64(0), val, "retention rows deleted should not increment when isLive=false")

	// isLive=true should add.
	m.RecordRetentionRowsDeleted("settlements", 5, true)
	val = getCounterValue(t, reg, "psm_retention_rows_deleted_total", lbls)
	assert.Equal(t, float64(5), val, "retention rows deleted should add count when isLive=true")
}
