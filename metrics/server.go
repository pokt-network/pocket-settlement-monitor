package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

const shutdownTimeout = 5 * time.Second

// Server serves HTTP endpoints for Prometheus metrics, health checks, and readiness.
type Server struct {
	addr        string
	httpServer  *http.Server
	registry    *prometheus.Registry
	wsConnected atomic.Bool
	dbCheck     func() bool
	logger      zerolog.Logger
}

// NewServer creates a new metrics HTTP server.
// wsConnected starts as false, dbCheck defaults to returning false.
func NewServer(addr string, registry *prometheus.Registry, logger zerolog.Logger) *Server {
	s := &Server{
		addr:     addr,
		registry: registry,
		dbCheck:  func() bool { return false },
		logger:   logger,
	}
	return s
}

// SetWSConnected updates the WebSocket connection status for the /ready endpoint.
func (s *Server) SetWSConnected(connected bool) {
	s.wsConnected.Store(connected)
}

// SetDBCheck sets the database readiness check function.
func (s *Server) SetDBCheck(fn func() bool) {
	s.dbCheck = fn
}

// healthResponse is the JSON body for /health.
type healthResponse struct {
	Status string `json:"status"`
}

// readyResponse is the JSON body for /ready.
type readyResponse struct {
	Status    string `json:"status"`
	Websocket bool   `json:"websocket"`
	DB        bool   `json:"db"`
}

// Start creates the HTTP server and starts listening.
// It blocks until the context is canceled or an error occurs.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// GET /metrics - Prometheus scrape endpoint.
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))

	// GET /health - Simple liveness probe (always returns 200).
	mux.HandleFunc("/health", s.handleHealth)

	// GET /ready - Readiness probe (200 only when WS+DB both ready).
	mux.HandleFunc("/ready", s.handleReady)

	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// Start the server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info().Str("addr", s.addr).Msg("metrics server starting")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for context cancellation or server error.
	select {
	case <-ctx.Done():
		return s.Stop(context.Background())
	case err := <-errCh:
		return err
	}
}

// Stop gracefully shuts down the HTTP server with a timeout.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	s.logger.Info().Msg("metrics server shutting down")
	return s.httpServer.Shutdown(shutdownCtx)
}

// Handler returns the configured HTTP handler for use in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	return mux
}

// handleHealth responds with 200 and {"status":"ok"} for liveness probes.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}

// handleReady responds with readiness status based on WebSocket and DB state.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	wsOK := s.wsConnected.Load()
	dbOK := s.dbCheck()

	resp := readyResponse{
		Websocket: wsOK,
		DB:        dbOK,
	}

	w.Header().Set("Content-Type", "application/json")
	if wsOK && dbOK {
		resp.Status = "ok"
		w.WriteHeader(http.StatusOK)
	} else {
		resp.Status = "not_ready"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(resp)
}
