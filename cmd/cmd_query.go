package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

var (
	qSupplier string
	qService  string
	qFrom     string
	qTo       string
	qLimit    int
	qOutput   string
)

func init() {
	rootCmd.AddCommand(queryCmd)

	queryCmd.PersistentFlags().StringVar(&qSupplier, "supplier", "", "Filter by supplier operator address")
	queryCmd.PersistentFlags().StringVar(&qService, "service", "", "Filter by service ID")
	queryCmd.PersistentFlags().StringVar(&qFrom, "from", "", "Start filter (block height or ISO 8601 date)")
	queryCmd.PersistentFlags().StringVar(&qTo, "to", "", "End filter (block height or ISO 8601 date)")
	queryCmd.PersistentFlags().IntVar(&qLimit, "limit", 0, "Maximum number of results (0 = unlimited)")
	queryCmd.PersistentFlags().StringVarP(&qOutput, "output", "o", "table", "Output format: table, json, csv")
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query settlement data from the local database",
	Long: `Query settlement data stored in the local SQLite database.
Use subcommands to query specific event types (settlements, slashes,
overservice, reimbursements) or aggregated summaries.`,
}

// openQueryStore opens the SQLite store in read mode for query commands.
// Retention is set to 0 since the query command does not perform cleanup.
func openQueryStore(cmd *cobra.Command) (store.Store, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := store.Open(ctx, cfg.Database.Path, 0, logger)
	if err != nil {
		return nil, fmt.Errorf("opening database for query: %w", err)
	}

	return db, nil
}

// parseQueryFilters parses the shared --from and --to flags into time and height filters.
// If a value parses as int64, it is treated as a block height filter.
// If a value parses as a date/time, it is treated as a timestamp filter.
// Empty values result in zero-value outputs (no filter).
func parseQueryFilters() (fromTime time.Time, toTime time.Time, fromHeight int64, toHeight int64, err error) {
	if qFrom != "" {
		if h, parseErr := strconv.ParseInt(qFrom, 10, 64); parseErr == nil {
			fromHeight = h
		} else if t, _, parseErr := parseFlexibleTime(qFrom); parseErr == nil {
			fromTime = t
		} else {
			err = fmt.Errorf("invalid --from value %q: must be a block height (integer) or ISO 8601 date", qFrom)
			return
		}
	}

	if qTo != "" {
		if h, parseErr := strconv.ParseInt(qTo, 10, 64); parseErr == nil {
			toHeight = h
		} else if t, _, parseErr := parseFlexibleTime(qTo); parseErr == nil {
			toTime = t
		} else {
			err = fmt.Errorf("invalid --to value %q: must be a block height (integer) or ISO 8601 date", qTo)
			return
		}
	}

	return
}

// validateOutputFormat checks that the --output flag has a valid value.
func validateOutputFormat() error {
	switch qOutput {
	case "table", "json", "csv":
		return nil
	default:
		return fmt.Errorf("invalid output format %q: must be table, json, or csv", qOutput)
	}
}

// truncAddr truncates an address for table display readability.
// Returns the full address if shorter than 15 characters.
func truncAddr(addr string) string {
	if len(addr) > 15 {
		return addr[:12] + "..."
	}
	return addr
}
