package metrics

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger creates a zerolog logger for test output.
func testLogger() zerolog.Logger {
	return zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.Disabled)
}

func TestServer_HealthEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := NewServer(":0", reg, testLogger())

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body healthResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body.Status)
}

func TestServer_ReadyEndpoint_NotReady(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := NewServer(":0", reg, testLogger())
	// Both wsConnected (default false) and dbCheck (default returns false) are not ready.

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body readyResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "not_ready", body.Status)
	assert.False(t, body.Websocket)
	assert.False(t, body.DB)
}

func TestServer_ReadyEndpoint_PartialReady(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := NewServer(":0", reg, testLogger())
	srv.SetWSConnected(true)
	// DB check still returns false (default).

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body readyResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "not_ready", body.Status)
	assert.True(t, body.Websocket)
	assert.False(t, body.DB)
}

func TestServer_ReadyEndpoint_FullyReady(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := NewServer(":0", reg, testLogger())
	srv.SetWSConnected(true)
	srv.SetDBCheck(func() bool { return true })

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body readyResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body.Status)
	assert.True(t, body.Websocket)
	assert.True(t, body.DB)
}

func TestServer_MetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	labels := &LabelConfig{}
	m := NewMetrics(reg, labels)
	// Touch a metric so it appears in output.
	m.SetBlockHeight(100)

	srv := NewServer(":0", reg, testLogger())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	body := string(bodyBytes)
	assert.Contains(t, body, "psm_", "metrics output should contain psm_ prefix")
	assert.Contains(t, body, "psm_current_block_height", "should contain current_block_height metric")
}
