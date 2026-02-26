package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/logging"
)

var (
	cfgFile string
	cfg     config.Config
	logger  logging.Logger
)

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "pocket-settlement-monitor",
	Short: "Monitor Pocket Network settlement events",
	Long: `pocket-settlement-monitor subscribes to poktroll tokenomics settlement events
via CometBFT WebSocket, persists them to SQLite, and exposes Prometheus metrics
and Discord notifications.`,
	SilenceUsage:  true, // Don't print usage on RunE errors (runtime errors are not usage errors).
	SilenceErrors: true, // We handle error printing in Execute().
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for version command.
		if cmd.Name() == "version" {
			return nil
		}

		var err error
		if cfgFile != "" {
			cfg, err = config.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
		} else {
			cfg = config.DefaultConfig()
		}

		logger = logging.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
}

// Execute runs the root command with signal-aware context.
// All subcommands receive a context that cancels on SIGINT/SIGTERM,
// enabling graceful shutdown across the entire CLI.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		// Signal-driven context cancellation is graceful shutdown, not an error.
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
