package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for pocket-settlement-monitor.
type Config struct {
	CometBFT      CometBFTConfig      `yaml:"cometbft"`
	Suppliers     SuppliersConfig     `yaml:"suppliers"`
	Database      DatabaseConfig      `yaml:"database"`
	Metrics       MetricsConfig       `yaml:"metrics"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Backfill      BackfillConfig      `yaml:"backfill"`
	Logging       LoggingConfig       `yaml:"logging"`
}

// CometBFTConfig holds the CometBFT RPC connection settings.
type CometBFTConfig struct {
	RPCURL             string        `yaml:"rpc_url"`
	ReconnectBaseDelay time.Duration `yaml:"reconnect_base_delay"`
	ReconnectMaxDelay  time.Duration `yaml:"reconnect_max_delay"`
	HeartbeatTimeout   time.Duration `yaml:"heartbeat_timeout"`
}

// SuppliersConfig specifies which supplier addresses to monitor.
// If both KeysFile and Addresses are empty, all suppliers are monitored (explorer mode).
type SuppliersConfig struct {
	KeysFile  string   `yaml:"keys_file"`
	Addresses []string `yaml:"addresses"`
}

// DatabaseConfig holds SQLite storage settings.
type DatabaseConfig struct {
	Path      string        `yaml:"path"`
	WALMode   bool          `yaml:"wal_mode"`
	Retention time.Duration `yaml:"retention"`
}

// MetricsConfig holds Prometheus metrics server settings.
type MetricsConfig struct {
	Enabled bool                `yaml:"enabled"`
	Addr    string              `yaml:"addr"`
	Labels  MetricsLabelsConfig `yaml:"labels"`
}

// MetricsLabelsConfig controls which optional label dimensions are included on metrics.
// All toggles default to false (minimal cardinality out of the box).
type MetricsLabelsConfig struct {
	IncludeSupplier    bool `yaml:"include_supplier"`
	IncludeService     bool `yaml:"include_service"`
	IncludeApplication bool `yaml:"include_application"`
}

// NotificationsConfig holds all Discord webhook notification settings in a single flat section.
// Combines settlement notifications, critical slash alerts, and operational alerts.
type NotificationsConfig struct {
	// Webhook URLs
	WebhookURL         string `yaml:"webhook_url"`          // Default / settlement webhook
	CriticalWebhookURL string `yaml:"critical_webhook_url"` // Slashes (optional, falls back to webhook_url)
	OpsWebhookURL      string `yaml:"ops_webhook_url"`      // Ops alerts (optional, falls back to webhook_url)

	// Settlement toggles
	NotifySettlements bool `yaml:"notify_settlements"`
	NotifyExpirations bool `yaml:"notify_expirations"`
	NotifySlashes     bool `yaml:"notify_slashes"`
	NotifyDiscards    bool `yaml:"notify_discards"`
	NotifyOverservice bool `yaml:"notify_overservice"`
	HourlySummary     bool `yaml:"hourly_summary"`
	DailySummary      bool `yaml:"daily_summary"`

	// Ops toggles
	NotifyConnection bool `yaml:"notify_connection"` // started, connected, disconnected
	NotifyGap        bool `yaml:"notify_gap"`        // gap detected, backfill started/completed
	NotifyHealth     bool `yaml:"notify_health"`     // node unreachable, channel overflow
}

// EffectiveCriticalWebhookURL returns CriticalWebhookURL if set, otherwise falls back to WebhookURL.
func (n *NotificationsConfig) EffectiveCriticalWebhookURL() string {
	if n.CriticalWebhookURL != "" {
		return n.CriticalWebhookURL
	}
	return n.WebhookURL
}

// EffectiveOpsWebhookURL returns OpsWebhookURL if set, otherwise falls back to WebhookURL.
func (n *NotificationsConfig) EffectiveOpsWebhookURL() string {
	if n.OpsWebhookURL != "" {
		return n.OpsWebhookURL
	}
	return n.WebhookURL
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// BackfillConfig holds settings for gap recovery backfill behavior.
type BackfillConfig struct {
	Delay            time.Duration `yaml:"delay"`
	ProgressInterval int           `yaml:"progress_interval"`

	// LiveCatchupThreshold controls when gap-recovered blocks are treated as
	// "live" rather than "historical backfill". If a block's timestamp is
	// within this duration of the current wall-clock time, it is processed
	// with isLive=true — meaning Prometheus counters increment and Discord
	// notifications fire.
	//
	// This exists because CometBFT sometimes fails to deliver settlement
	// blocks via the WebSocket subscription (they are too heavy / slow to
	// finalize). The gap detector catches them, but without this threshold
	// they would be treated as cold backfill and silently ignored by metrics
	// and notifications.
	//
	// Set to 0 to disable (all gap-recovered blocks treated as backfill).
	// Default: 20 minutes.
	LiveCatchupThreshold time.Duration `yaml:"live_catchup_threshold"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		CometBFT: CometBFTConfig{
			RPCURL:             "tcp://localhost:26657",
			ReconnectBaseDelay: 1 * time.Second,
			ReconnectMaxDelay:  30 * time.Second,
			HeartbeatTimeout:   15 * time.Minute,
		},
		Database: DatabaseConfig{
			Path:      "./settlement-monitor.db",
			WALMode:   true,
			Retention: 30 * 24 * time.Hour, // 30 days
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Addr:    ":9090",
		},
		Notifications: NotificationsConfig{
			NotifyExpirations: true,
			NotifySlashes:     true,
			NotifyDiscards:    true,
			NotifyOverservice: true,
			HourlySummary:     true,
			DailySummary:      true,
			// Ops toggles default to false (opt-in per locked decision).
			// All webhook URLs default to empty (makes all sends no-ops).
		},
		Backfill: BackfillConfig{
			Delay:                100 * time.Millisecond, // small delay per locked decision
			ProgressInterval:     100,                    // log every 100 blocks per locked decision
			LiveCatchupThreshold: 20 * time.Minute,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// LoadConfig reads a YAML config file and returns a Config.
// Missing fields are filled with defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// Validate checks the configuration for invalid values.
func (c *Config) Validate() error {
	var errs []string

	if c.CometBFT.RPCURL == "" {
		errs = append(errs, "cometbft.rpc_url is required")
	}
	if c.CometBFT.ReconnectBaseDelay <= 0 {
		errs = append(errs, "cometbft.reconnect_base_delay must be positive")
	}
	if c.CometBFT.ReconnectMaxDelay <= 0 {
		errs = append(errs, "cometbft.reconnect_max_delay must be positive")
	}
	if c.Database.Path == "" {
		errs = append(errs, "database.path is required")
	}
	if c.Metrics.Enabled && c.Metrics.Addr == "" {
		errs = append(errs, "metrics.addr is required when metrics are enabled")
	}
	if c.CometBFT.ReconnectBaseDelay > c.CometBFT.ReconnectMaxDelay {
		errs = append(errs, "cometbft.reconnect_base_delay must be <= reconnect_max_delay")
	}
	if c.CometBFT.HeartbeatTimeout <= 0 {
		errs = append(errs, "cometbft.heartbeat_timeout must be positive")
	}
	validSchemes := map[string]bool{"tcp": true, "http": true, "https": true}
	if u, parseErr := url.Parse(c.CometBFT.RPCURL); c.CometBFT.RPCURL != "" && (parseErr != nil || !validSchemes[u.Scheme]) {
		errs = append(errs, "cometbft.rpc_url must use tcp://, http://, or https:// scheme")
	}
	if c.Database.Retention < 0 {
		errs = append(errs, "database.retention must be non-negative (0 = keep forever)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	return nil
}

// IsMonitorAll returns true when no supplier filtering is configured.
func (c *Config) IsMonitorAll() bool {
	return c.Suppliers.KeysFile == "" && len(c.Suppliers.Addresses) == 0
}
