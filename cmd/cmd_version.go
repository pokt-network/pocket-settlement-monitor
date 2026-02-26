package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("pocket-settlement-monitor %s\n", version.Version)
		fmt.Printf("  commit:     %s\n", version.Commit)
		fmt.Printf("  build date: %s\n", version.BuildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
