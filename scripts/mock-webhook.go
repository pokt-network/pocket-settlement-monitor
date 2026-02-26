// mock-webhook.go — Fake Discord webhook receiver for integration testing.
//
// Mimics the Discord Execute Webhook endpoint. Accepts POST requests on
// configurable paths, pretty-prints the JSON payload, and returns 204
// (Discord's success response).
//
// Usage:
//
//	go run scripts/mock-webhook.go [-port 8888]
//
// Endpoints:
//
//	POST /webhook          — Settlement webhook (notifications.webhook_url)
//	POST /webhook-critical — Critical webhook for slash alerts (notifications.critical_webhook_url)
//	POST /webhook-ops      — Operational alerts webhook (notifications.ops_webhook_url)
//	GET  /requests         — List all received payloads as JSON array
//	GET  /requests/count   — Number of requests received
//	POST /reset            — Clear all captured requests
//
// Config example:
//
//	notifications:
//	  webhook_url: "http://localhost:8888/webhook"
//	  critical_webhook_url: "http://localhost:8888/webhook-critical"
//	  ops_webhook_url: "http://localhost:8888/webhook-ops"
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	requestCount atomic.Int64
	mu           sync.Mutex
	captured     []capturedRequest
)

type capturedRequest struct {
	Number    int64                  `json:"number"`
	Timestamp string                 `json:"timestamp"`
	Path      string                 `json:"path"`
	Payload   map[string]interface{} `json:"payload"`
}

func main() {
	port := "8888"
	if len(os.Args) > 2 && os.Args[1] == "-port" {
		port = os.Args[2]
	}

	http.HandleFunc("/webhook", webhookHandler)
	http.HandleFunc("/webhook-critical", webhookHandler)
	http.HandleFunc("/webhook-ops", webhookHandler)
	http.HandleFunc("/requests", listHandler)
	http.HandleFunc("/requests/count", countHandler)
	http.HandleFunc("/reset", resetHandler)

	fmt.Printf("Mock Discord webhook listening on :%s\n", port)
	fmt.Printf("  Settlement: http://localhost:%s/webhook          (notifications.webhook_url)\n", port)
	fmt.Printf("  Critical:   http://localhost:%s/webhook-critical (notifications.critical_webhook_url)\n", port)
	fmt.Printf("  Ops:        http://localhost:%s/webhook-ops      (notifications.ops_webhook_url)\n", port)
	fmt.Printf("  List:     http://localhost:%s/requests\n", port)
	fmt.Printf("  Count:    http://localhost:%s/requests/count\n", port)
	fmt.Printf("  Reset:    curl -X POST http://localhost:%s/reset\n", port)
	fmt.Println()
	fmt.Println("Waiting for requests...")
	fmt.Println()

	srv := &http.Server{Addr: ":" + port}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Printf("\nShutting down (received %d requests)\n", requestCount.Load())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	n := requestCount.Add(1)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		http.Error(w, "read error", 500)
		return
	}
	defer r.Body.Close()

	ts := time.Now().Format("15:04:05")

	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Printf("--- Request #%d [%s] %s (raw, not JSON) ---\n%s\n\n",
			n, ts, r.URL.Path, string(body))
	} else {
		pretty, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Printf("--- Request #%d [%s] %s ---\n%s\n\n",
			n, ts, r.URL.Path, string(pretty))

		mu.Lock()
		captured = append(captured, capturedRequest{
			Number:    n,
			Timestamp: ts,
			Path:      r.URL.Path,
			Payload:   parsed,
		})
		mu.Unlock()
	}

	// Discord returns 204 No Content on success.
	w.WriteHeader(http.StatusNoContent)
}

func listHandler(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	data := make([]capturedRequest, len(captured))
	copy(data, captured)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}

func countHandler(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	n := len(captured)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"count":%d}`, n)
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	captured = nil
	mu.Unlock()
	requestCount.Store(0)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"cleared"}`)
}
