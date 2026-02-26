package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig_HasRequiredFields(t *testing.T) {
	cfg := DefaultConfig()
	assert.NotEmpty(t, cfg.CometBFT.RPCURL)
	assert.NotEmpty(t, cfg.Database.Path)
	assert.Greater(t, cfg.CometBFT.ReconnectBaseDelay, time.Duration(0))
	assert.Greater(t, cfg.CometBFT.ReconnectMaxDelay, time.Duration(0))
}

func TestDefaultConfig_PassesValidation(t *testing.T) {
	cfg := DefaultConfig()
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestLoadConfig_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
cometbft:
  rpc_url: "tcp://example.com:26657"
database:
  path: "./test.db"
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "tcp://example.com:26657", cfg.CometBFT.RPCURL)
	assert.Equal(t, "./test.db", cfg.Database.Path)
	// Defaults should be preserved for unset fields.
	assert.Greater(t, cfg.CometBFT.ReconnectBaseDelay, time.Duration(0))
}

func TestLoadConfig_OverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
cometbft:
  rpc_url: "https://custom-rpc.example.com"
`), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "https://custom-rpc.example.com", cfg.CometBFT.RPCURL)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	err := os.WriteFile(path, []byte("{{{{not yaml}}}}"), 0644)
	require.NoError(t, err)

	_, err = LoadConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestValidate_MissingRPCURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CometBFT.RPCURL = ""
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rpc_url is required")
}

func TestValidate_NegativeReconnectDelay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CometBFT.ReconnectBaseDelay = -1 * time.Second
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconnect_base_delay must be positive")
}

func TestValidate_MissingDBPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Database.Path = ""
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database.path is required")
}

func TestValidate_MetricsEnabledNoAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Addr = ""
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metrics.addr is required when metrics are enabled")
}

func TestValidate_MetricsDisabledNoAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Metrics.Enabled = false
	cfg.Metrics.Addr = ""
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_NegativeRetention(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Database.Retention = -1 * time.Hour
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "retention must be non-negative")
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CometBFT.RPCURL = ""
	cfg.Database.Path = ""
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rpc_url is required")
	assert.Contains(t, err.Error(), "database.path is required")
}

func TestIsMonitorAll_NoSuppliers(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.IsMonitorAll())
}

func TestIsMonitorAll_WithAddresses(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Suppliers.Addresses = []string{"pokt1abc"}
	assert.False(t, cfg.IsMonitorAll())
}

func TestIsMonitorAll_WithKeysFile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Suppliers.KeysFile = "/path/to/keys.yaml"
	assert.False(t, cfg.IsMonitorAll())
}
