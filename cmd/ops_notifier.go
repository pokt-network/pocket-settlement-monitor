package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/pokt-network/pocket-settlement-monitor/config"
)

// ColorOps is the purple embed color used for all operational notifications.
const ColorOps = 0x9B59B6

const opsUsername = "pocket-settlement-monitor [ops]"

// OpsNotifier sends Discord webhook notifications for operational events.
// Separate from the settlement Notifier (notify package) because:
// - Different webhook URL (ops vs settlements)
// - Different formatting (purple embeds, different content)
// - Different trigger points (lifecycle events, not block events)
// - Independently toggleable categories
type OpsNotifier struct {
	webhookURL string
	cfg        config.NotificationsConfig
	client     *http.Client
	logger     zerolog.Logger
}

// opsEmbed is a minimal Discord embed struct for operational notifications.
type opsEmbed struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Color       int        `json:"color"`
	Footer      *opsFooter `json:"footer,omitempty"`
	Timestamp   string     `json:"timestamp,omitempty"`
}

// opsFooter is the footer sub-object for operational embeds.
type opsFooter struct {
	Text string `json:"text"`
}

// opsPayload is the JSON body sent to Discord's Execute Webhook endpoint.
type opsPayload struct {
	Username string     `json:"username"`
	Embeds   []opsEmbed `json:"embeds"`
}

// NewOpsNotifier creates a new OpsNotifier. If the effective ops webhook URL is empty,
// all methods become no-ops.
func NewOpsNotifier(cfg config.NotificationsConfig, logger zerolog.Logger) *OpsNotifier {
	return &OpsNotifier{
		webhookURL: cfg.EffectiveOpsWebhookURL(),
		cfg:        cfg,
		client:     &http.Client{Timeout: 10 * time.Second},
		logger:     logger.With().Str("component", "ops_notifier").Logger(),
	}
}

// SendMonitorStarted sends a "Settlement Monitor Started" notification (connection category).
func (o *OpsNotifier) SendMonitorStarted() {
	if !o.canSend(o.cfg.NotifyConnection) {
		return
	}
	o.send(opsEmbed{
		Title:       "Settlement Monitor Started",
		Description: "The settlement monitor process has started and is initializing.",
		Color:       ColorOps,
		Footer:      &opsFooter{Text: opsUsername},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	})
}

// SendWebSocketConnected sends a "WebSocket Connected" notification (connection category).
func (o *OpsNotifier) SendWebSocketConnected(rpcURL string) {
	if !o.canSend(o.cfg.NotifyConnection) {
		return
	}
	o.send(opsEmbed{
		Title:       "WebSocket Connected",
		Description: fmt.Sprintf("Connected to CometBFT WebSocket at `%s`.", rpcURL),
		Color:       ColorOps,
		Footer:      &opsFooter{Text: opsUsername},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	})
}

// SendWebSocketDisconnected sends a "WebSocket Disconnected" notification (connection category).
func (o *OpsNotifier) SendWebSocketDisconnected(lastHeight int64, err error) {
	if !o.canSend(o.cfg.NotifyConnection) {
		return
	}
	desc := fmt.Sprintf("Disconnected from CometBFT WebSocket.\n**Last height:** %d", lastHeight)
	if err != nil {
		desc += fmt.Sprintf("\n**Error:** %s", err.Error())
	}
	o.send(opsEmbed{
		Title:       "WebSocket Disconnected",
		Description: desc,
		Color:       ColorOps,
		Footer:      &opsFooter{Text: opsUsername},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	})
}

// SendGapDetected sends a "Gap Detected" notification (gap category).
func (o *OpsNotifier) SendGapDetected(fromHeight, toHeight, gapSize int64) {
	if !o.canSend(o.cfg.NotifyGap) {
		return
	}
	o.send(opsEmbed{
		Title: "Gap Detected",
		Description: fmt.Sprintf("Detected a gap of **%d blocks** (height %d to %d).",
			gapSize, fromHeight, toHeight),
		Color:     ColorOps,
		Footer:    &opsFooter{Text: opsUsername},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// SendBackfillStarted sends a "Backfill Started" notification (gap category).
func (o *OpsNotifier) SendBackfillStarted(fromHeight, toHeight int64) {
	if !o.canSend(o.cfg.NotifyGap) {
		return
	}
	o.send(opsEmbed{
		Title: "Backfill Started",
		Description: fmt.Sprintf("Starting backfill from height %d to %d (%d blocks).",
			fromHeight, toHeight, toHeight-fromHeight+1),
		Color:     ColorOps,
		Footer:    &opsFooter{Text: opsUsername},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// SendBackfillCompleted sends a "Backfill Completed" notification (gap category).
func (o *OpsNotifier) SendBackfillCompleted(fromHeight, toHeight int64, duration time.Duration) {
	if !o.canSend(o.cfg.NotifyGap) {
		return
	}
	o.send(opsEmbed{
		Title: "Backfill Completed",
		Description: fmt.Sprintf("Backfill from height %d to %d completed in %s.",
			fromHeight, toHeight, duration.Round(time.Millisecond)),
		Color:     ColorOps,
		Footer:    &opsFooter{Text: opsUsername},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// SendHealthWarning sends a generic health warning notification (health category).
func (o *OpsNotifier) SendHealthWarning(title, description string) {
	if !o.canSend(o.cfg.NotifyHealth) {
		return
	}
	o.send(opsEmbed{
		Title:       title,
		Description: description,
		Color:       ColorOps,
		Footer:      &opsFooter{Text: opsUsername},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	})
}

// canSend returns true if the webhook URL is configured and the category toggle is enabled.
func (o *OpsNotifier) canSend(categoryEnabled bool) bool {
	return o.webhookURL != "" && categoryEnabled
}

// send posts a single embed to the Discord webhook. Errors are logged but not propagated
// since operational notifications are best-effort.
func (o *OpsNotifier) send(e opsEmbed) {
	payload := opsPayload{
		Username: opsUsername,
		Embeds:   []opsEmbed{e},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		o.logger.Error().Err(err).Str("title", e.Title).Msg("failed to marshal ops notification payload")
		return
	}

	resp, err := o.client.Post(o.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		o.logger.Error().Err(err).Str("title", e.Title).Msg("failed to send ops notification")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		o.logger.Error().
			Int("status_code", resp.StatusCode).
			Str("title", e.Title).
			Msg("ops notification webhook returned non-2xx status")
	}
}
