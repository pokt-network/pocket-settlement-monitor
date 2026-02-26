package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/metrics"
	"github.com/pokt-network/pocket-settlement-monitor/processor"
	"github.com/pokt-network/pocket-settlement-monitor/store"
)

const (
	// channelBufferSize is the capacity of the notification channel.
	// Must be large enough to absorb Discord 429 retry delays without dropping.
	channelBufferSize = 64

	// maxRetries is the maximum number of retry attempts for a single notification.
	maxRetries = 3

	// defaultRetryWait is the fallback wait duration when 429 response lacks retry_after.
	defaultRetryWait = 2 * time.Second

	// drainTimeout is the maximum time allowed to drain remaining messages on shutdown.
	drainTimeout = 5 * time.Second
)

// notification is an internal message queued to the sender channel.
type notification struct {
	webhookURL string
	payload    webhookPayload
	notifType  string // "block", "slash_critical", "hourly_summary", "daily_summary"
}

// rateLimitResponse represents Discord's 429 rate limit response body.
type rateLimitResponse struct {
	Message    string  `json:"message"`
	RetryAfter float64 `json:"retry_after"`
	Global     bool    `json:"global"`
}

// Notifier dispatches Discord webhook notifications for settlement events.
// It implements processor.BlockNotifier with a non-blocking buffered channel
// and a background sender goroutine with 429 retry logic.
type Notifier struct {
	webhookURL         string
	criticalWebhookURL string
	cfg                config.NotificationsConfig
	formatter          *discordFormatter
	client             *http.Client
	ch                 chan notification
	metrics            *metrics.Metrics
	store              store.Store
	logger             zerolog.Logger
	done               chan struct{}
	wg                 sync.WaitGroup
	lastHourlySummary  time.Time
	lastDailySummary   time.Time
}

// Compile-time interface check: Notifier must implement processor.BlockNotifier.
var _ processor.BlockNotifier = (*Notifier)(nil)

// NewNotifier creates a Notifier wired to the given notifications config, metrics, store, and logger.
func NewNotifier(cfg config.NotificationsConfig, m *metrics.Metrics, s store.Store, logger zerolog.Logger) *Notifier {
	return &Notifier{
		webhookURL:         cfg.WebhookURL,
		criticalWebhookURL: cfg.EffectiveCriticalWebhookURL(),
		cfg:                cfg,
		formatter:          newDiscordFormatter(cfg),
		client:             &http.Client{Timeout: 10 * time.Second},
		ch:                 make(chan notification, channelBufferSize),
		metrics:            m,
		store:              s,
		logger:             logging.ForComponent(logger, "notifier"),
		done:               make(chan struct{}),
	}
}

// Start launches the background sender goroutine.
func (n *Notifier) Start(ctx context.Context) {
	n.wg.Add(1)
	go n.senderLoop(ctx)
}

// Stop signals the sender goroutine to drain remaining messages and exit.
// Blocks until the goroutine finishes or the drain timeout expires.
func (n *Notifier) Stop() {
	close(n.done)
	n.wg.Wait()
}

// NotifyBlock implements processor.BlockNotifier. It builds embeds for block events,
// checks summary boundaries, and enqueues notifications to the async sender.
func (n *Notifier) NotifyBlock(
	ctx context.Context,
	height int64,
	settlements []store.Settlement,
	overservices []store.OverserviceEvent,
	reimbursements []store.ReimbursementEvent,
) {
	// Determine block timestamp from the first event with a non-zero timestamp.
	var ts time.Time
	for _, s := range settlements {
		if !s.BlockTimestamp.IsZero() {
			ts = s.BlockTimestamp
			break
		}
	}
	if ts.IsZero() {
		for _, o := range overservices {
			if !o.BlockTimestamp.IsZero() {
				ts = o.BlockTimestamp
				break
			}
		}
	}

	// Check summary boundaries BEFORE building block embeds.
	if !ts.IsZero() {
		n.checkSummaryBoundary(ctx, height, ts)
	}

	// Build block embeds.
	payloads := n.formatter.buildBlockEmbeds(height, ts, settlements, overservices, reimbursements)
	if len(payloads) == 0 {
		return
	}

	// Enqueue each payload to normal webhook.
	for _, p := range payloads {
		n.enqueue(notification{
			webhookURL: n.webhookURL,
			payload:    p,
			notifType:  "block",
		})

		// Slash events also go to the critical webhook.
		if p.isSlash && n.criticalWebhookURL != "" {
			n.enqueue(notification{
				webhookURL: n.criticalWebhookURL,
				payload:    p,
				notifType:  "slash_critical",
			})
		}
	}
}

// enqueue performs a non-blocking send to the notification channel.
// If the channel is full, the message is dropped and a warning is logged.
func (n *Notifier) enqueue(msg notification) {
	select {
	case n.ch <- msg:
	default:
		n.logger.Warn().
			Str("type", msg.notifType).
			Str("webhook", msg.webhookURL).
			Msg("notification channel full, dropping message")
		n.metrics.RecordDiscordNotificationError(msg.notifType, true)
	}
}

// senderLoop processes notifications from the channel until shutdown.
func (n *Notifier) senderLoop(ctx context.Context) {
	defer n.wg.Done()

	for {
		select {
		case msg := <-n.ch:
			if err := n.sendWithRetry(ctx, msg); err != nil {
				n.logger.Error().Err(err).
					Str("type", msg.notifType).
					Msg("failed to send discord notification")
				n.metrics.RecordDiscordNotificationError(msg.notifType, true)
			} else {
				n.metrics.RecordDiscordNotification(msg.notifType, true)
			}

		case <-n.done:
			// Drain remaining messages with timeout.
			drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
			defer cancel()
			n.drain(drainCtx)
			return

		case <-ctx.Done():
			return
		}
	}
}

// drain processes remaining messages in the channel until empty or context expires.
func (n *Notifier) drain(ctx context.Context) {
	for {
		select {
		case msg := <-n.ch:
			if err := n.sendWithRetry(ctx, msg); err != nil {
				n.logger.Error().Err(err).
					Str("type", msg.notifType).
					Msg("failed to send discord notification during drain")
				n.metrics.RecordDiscordNotificationError(msg.notifType, true)
			} else {
				n.metrics.RecordDiscordNotification(msg.notifType, true)
			}
		case <-ctx.Done():
			return
		default:
			// Channel is empty, drain complete.
			return
		}
	}
}

// sendWithRetry attempts to send a notification with up to maxRetries retries on 429 responses.
func (n *Notifier) sendWithRetry(ctx context.Context, msg notification) error {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := n.doPost(ctx, msg.webhookURL, msg.payload)
		if err != nil {
			return fmt.Errorf("HTTP request failed: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent:
			resp.Body.Close()
			return nil

		case http.StatusTooManyRequests:
			wait := n.parseRetryAfter(resp)
			resp.Body.Close()

			if attempt == maxRetries {
				return fmt.Errorf("rate limited after %d retries", maxRetries+1)
			}

			n.logger.Warn().
				Dur("retry_after", wait).
				Int("attempt", attempt+1).
				Msg("discord rate limited, waiting")

			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}

		default:
			// Non-retryable error: discard body and return.
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
		}
	}

	return fmt.Errorf("max retries exceeded")
}

// doPost sends a JSON-encoded payload to the given URL.
func (n *Notifier) doPost(ctx context.Context, url string, payload webhookPayload) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return n.client.Do(req)
}

// parseRetryAfter extracts the retry duration from a 429 response.
// Tries JSON body first, then Retry-After header, then falls back to defaultRetryWait.
func (n *Notifier) parseRetryAfter(resp *http.Response) time.Duration {
	body, err := io.ReadAll(resp.Body)
	if err == nil && len(body) > 0 {
		var rl rateLimitResponse
		if json.Unmarshal(body, &rl) == nil && rl.RetryAfter > 0 {
			return time.Duration(rl.RetryAfter * float64(time.Second))
		}
	}

	// Fallback: check Retry-After header.
	if hdr := resp.Header.Get("Retry-After"); hdr != "" {
		if d, err := time.ParseDuration(hdr + "s"); err == nil {
			return d
		}
	}

	return defaultRetryWait
}

// checkSummaryBoundary checks if block timestamps cross hourly or daily boundaries
// and enqueues summary notifications when they do.
func (n *Notifier) checkSummaryBoundary(ctx context.Context, height int64, ts time.Time) {
	currentHour := ts.UTC().Truncate(time.Hour)
	currentDay := time.Date(ts.UTC().Year(), ts.UTC().Month(), ts.UTC().Day(), 0, 0, 0, 0, time.UTC)

	// Hourly boundary check.
	if n.lastHourlySummary.IsZero() {
		// First block seen -- initialize tracker without triggering summary.
		n.lastHourlySummary = currentHour
	} else if !currentHour.Equal(n.lastHourlySummary) {
		// Hour boundary crossed -- send summary for the completed hour.
		if n.cfg.HourlySummary {
			summary, err := n.store.GetHourlySummaryNetwork(ctx, n.lastHourlySummary)
			if err != nil {
				n.logger.Error().Err(err).
					Time("hour", n.lastHourlySummary).
					Msg("failed to get hourly summary for notification")
			} else {
				payload := n.formatter.buildHourlySummaryEmbed(summary)
				n.enqueue(notification{
					webhookURL: n.webhookURL,
					payload:    payload,
					notifType:  "hourly_summary",
				})
			}
		}
		n.lastHourlySummary = currentHour
	}

	// Daily boundary check.
	if n.lastDailySummary.IsZero() {
		// First block seen -- initialize tracker without triggering summary.
		n.lastDailySummary = currentDay
	} else if !currentDay.Equal(n.lastDailySummary) {
		// Day boundary crossed -- send summary for the completed day.
		if n.cfg.DailySummary {
			summary, err := n.store.GetDailySummaryNetwork(ctx, n.lastDailySummary)
			if err != nil {
				n.logger.Error().Err(err).
					Time("day", n.lastDailySummary).
					Msg("failed to get daily summary for notification")
			} else {
				// Get previous day for comparison.
				prevDay, err := n.store.GetDailySummaryNetwork(ctx, n.lastDailySummary.AddDate(0, 0, -1))
				if err != nil {
					n.logger.Warn().Err(err).
						Msg("failed to get previous day summary for comparison")
					prevDay = store.DailySummaryNetwork{}
				}

				// Build per-supplier breakdown.
				supplierBreakdown := n.buildSupplierBreakdown(ctx, n.lastDailySummary)

				payload := n.formatter.buildDailySummaryEmbed(summary, prevDay, supplierBreakdown)
				n.enqueue(notification{
					webhookURL: n.webhookURL,
					payload:    payload,
					notifType:  "daily_summary",
				})
			}
		}
		n.lastDailySummary = currentDay
	}
}

// buildSupplierBreakdown queries settlements for the given day and builds a
// per-supplier summary string (top 10 by total claimed uPOKT, truncated addresses).
func (n *Notifier) buildSupplierBreakdown(ctx context.Context, dayStart time.Time) string {
	dayEnd := dayStart.AddDate(0, 0, 1)
	settlements, err := n.store.QuerySettlementsForPeriod(ctx, dayStart, dayEnd)
	if err != nil {
		n.logger.Warn().Err(err).Msg("failed to query settlements for supplier breakdown")
		return ""
	}

	if len(settlements) == 0 {
		return ""
	}

	// Group by supplier operator address.
	type supplierStats struct {
		addr         string
		claimedUpokt int64
	}
	grouped := make(map[string]int64)
	for _, s := range settlements {
		grouped[s.SupplierOperatorAddress] += s.ClaimedUpokt
	}

	// Sort by claimed uPOKT descending.
	var stats []supplierStats
	for addr, claimed := range grouped {
		stats = append(stats, supplierStats{addr: addr, claimedUpokt: claimed})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].claimedUpokt > stats[j].claimedUpokt
	})

	// Take top 10.
	if len(stats) > 10 {
		stats = stats[:10]
	}

	// Build breakdown string.
	var lines []string
	for _, s := range stats {
		lines = append(lines, fmt.Sprintf("%s: %s", truncateAddress(s.addr), formatPOKT(s.claimedUpokt)))
	}

	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}
