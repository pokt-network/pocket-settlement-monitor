package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pokt-network/pocket-settlement-monitor/config"
)

// capturedPayload holds the decoded webhook request for test assertions.
type capturedPayload struct {
	payload   opsPayload
	callCount int
}

// newTestOpsServer creates an httptest server that captures Discord webhook payloads.
func newTestOpsServer(t *testing.T, captured *capturedPayload) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		defer r.Body.Close()

		err = json.Unmarshal(body, &captured.payload)
		require.NoError(t, err)
		captured.callCount++

		w.WriteHeader(http.StatusNoContent)
	}))
}

// newTestOpsNotifier creates an OpsNotifier pointing at the test server.
func newTestOpsNotifier(serverURL string, cfg config.NotificationsConfig) *OpsNotifier {
	cfg.WebhookURL = serverURL
	return NewOpsNotifier(cfg, zerolog.Nop())
}

func TestOpsNotifier_SendMonitorStarted(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyConnection: true})
	notifier.SendMonitorStarted()

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "Settlement Monitor Started", captured.payload.Embeds[0].Title)
	assert.Equal(t, ColorOps, captured.payload.Embeds[0].Color)
}

func TestOpsNotifier_SendWebSocketConnected(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyConnection: true})
	notifier.SendWebSocketConnected("tcp://localhost:26657")

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "WebSocket Connected", captured.payload.Embeds[0].Title)
	assert.Contains(t, captured.payload.Embeds[0].Description, "tcp://localhost:26657")
}

func TestOpsNotifier_SendWebSocketDisconnected_WithError(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyConnection: true})
	notifier.SendWebSocketDisconnected(12345, fmt.Errorf("connection reset"))

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "WebSocket Disconnected", captured.payload.Embeds[0].Title)
	assert.Contains(t, captured.payload.Embeds[0].Description, "connection reset")
	assert.Contains(t, captured.payload.Embeds[0].Description, "12345")
}

func TestOpsNotifier_SendWebSocketDisconnected_NilError(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyConnection: true})
	notifier.SendWebSocketDisconnected(99999, nil)

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Contains(t, captured.payload.Embeds[0].Description, "99999")
	assert.NotContains(t, captured.payload.Embeds[0].Description, "Error")
}

func TestOpsNotifier_SendGapDetected(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyGap: true})
	notifier.SendGapDetected(100, 200, 100)

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "Gap Detected", captured.payload.Embeds[0].Title)
	assert.Contains(t, captured.payload.Embeds[0].Description, "100 blocks")
	assert.Contains(t, captured.payload.Embeds[0].Description, "100")
	assert.Contains(t, captured.payload.Embeds[0].Description, "200")
}

func TestOpsNotifier_SendBackfillStarted(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyGap: true})
	notifier.SendBackfillStarted(500, 600)

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "Backfill Started", captured.payload.Embeds[0].Title)
	assert.Contains(t, captured.payload.Embeds[0].Description, "500")
	assert.Contains(t, captured.payload.Embeds[0].Description, "600")
}

func TestOpsNotifier_SendBackfillCompleted(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyGap: true})
	notifier.SendBackfillCompleted(500, 600, 5*time.Second)

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "Backfill Completed", captured.payload.Embeds[0].Title)
	assert.Contains(t, captured.payload.Embeds[0].Description, "500")
	assert.Contains(t, captured.payload.Embeds[0].Description, "600")
	assert.Contains(t, captured.payload.Embeds[0].Description, "5s")
}

func TestOpsNotifier_SendHealthWarning(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{NotifyHealth: true})
	notifier.SendHealthWarning("Node Unreachable", "CometBFT node at tcp://localhost:26657 is not responding.")

	require.Equal(t, 1, captured.callCount)
	require.Len(t, captured.payload.Embeds, 1)
	assert.Equal(t, "Node Unreachable", captured.payload.Embeds[0].Title)
	assert.Equal(t, "CometBFT node at tcp://localhost:26657 is not responding.", captured.payload.Embeds[0].Description)
}

func TestOpsNotifier_NoOpWhenWebhookEmpty(t *testing.T) {
	// Create notifier with empty webhook URL — should never make HTTP calls.
	notifier := NewOpsNotifier(config.NotificationsConfig{
		NotifyConnection: true,
		NotifyGap:        true,
		NotifyHealth:     true,
	}, zerolog.Nop())

	// None of these should panic or make network calls.
	notifier.SendMonitorStarted()
	notifier.SendWebSocketConnected("tcp://localhost:26657")
	notifier.SendWebSocketDisconnected(100, nil)
	notifier.SendGapDetected(1, 10, 9)
	notifier.SendBackfillStarted(1, 10)
	notifier.SendBackfillCompleted(1, 10, time.Second)
	notifier.SendHealthWarning("test", "test")
}

func TestOpsNotifier_NoOpWhenCategoryDisabled(t *testing.T) {
	captured := &capturedPayload{}
	server := newTestOpsServer(t, captured)
	defer server.Close()

	// All categories disabled.
	notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{
		NotifyConnection: false,
		NotifyGap:        false,
		NotifyHealth:     false,
	})

	notifier.SendMonitorStarted()
	notifier.SendWebSocketConnected("tcp://localhost:26657")
	notifier.SendWebSocketDisconnected(100, nil)
	notifier.SendGapDetected(1, 10, 9)
	notifier.SendBackfillStarted(1, 10)
	notifier.SendBackfillCompleted(1, 10, time.Second)
	notifier.SendHealthWarning("test", "test")

	assert.Equal(t, 0, captured.callCount, "no HTTP calls should be made when categories are disabled")
}

func TestOpsNotifier_CategoryToggles(t *testing.T) {
	t.Run("connection only", func(t *testing.T) {
		captured := &capturedPayload{}
		server := newTestOpsServer(t, captured)
		defer server.Close()

		notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{
			NotifyConnection: true,
			NotifyGap:        false,
			NotifyHealth:     false,
		})

		notifier.SendMonitorStarted()              // connection → should send
		notifier.SendGapDetected(1, 10, 9)         // gap → should NOT send
		notifier.SendHealthWarning("test", "test") // health → should NOT send

		assert.Equal(t, 1, captured.callCount)
	})

	t.Run("gap only", func(t *testing.T) {
		captured := &capturedPayload{}
		server := newTestOpsServer(t, captured)
		defer server.Close()

		notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{
			NotifyConnection: false,
			NotifyGap:        true,
			NotifyHealth:     false,
		})

		notifier.SendMonitorStarted()              // connection → should NOT send
		notifier.SendBackfillStarted(1, 10)        // gap → should send
		notifier.SendHealthWarning("test", "test") // health → should NOT send

		assert.Equal(t, 1, captured.callCount)
	})

	t.Run("health only", func(t *testing.T) {
		captured := &capturedPayload{}
		server := newTestOpsServer(t, captured)
		defer server.Close()

		notifier := newTestOpsNotifier(server.URL, config.NotificationsConfig{
			NotifyConnection: false,
			NotifyGap:        false,
			NotifyHealth:     true,
		})

		notifier.SendMonitorStarted()              // connection → should NOT send
		notifier.SendGapDetected(1, 10, 9)         // gap → should NOT send
		notifier.SendHealthWarning("test", "test") // health → should send

		assert.Equal(t, 1, captured.callCount)
	})
}
