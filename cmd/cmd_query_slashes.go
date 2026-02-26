package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

func init() {
	queryCmd.AddCommand(querySlashesCmd)
}

var querySlashesCmd = &cobra.Command{
	Use:   "slashes",
	Short: "Query slashed settlement events",
	Long: `Query slashed settlement events from the local database.
Automatically filters by event_type='slashed'.`,
	RunE: runQuerySlashes,
}

func runQuerySlashes(cmd *cobra.Command, _ []string) error {
	if err := validateOutputFormat(); err != nil {
		return err
	}

	db, err := openQueryStore(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	fromTime, toTime, fromHeight, toHeight, err := parseQueryFilters()
	if err != nil {
		return err
	}

	filters := store.SettlementFilters{
		EventType:               "slashed",
		SupplierOperatorAddress: qSupplier,
		ServiceID:               qService,
		FromTime:                fromTime,
		ToTime:                  toTime,
		FromHeight:              fromHeight,
		ToHeight:                toHeight,
		Limit:                   qLimit,
	}

	ctx := cmd.Context()
	results, err := db.QuerySettlementsFiltered(ctx, filters)
	if err != nil {
		return fmt.Errorf("querying slashes: %w", err)
	}

	columns := []string{"HEIGHT", "TIMESTAMP", "SUPPLIER", "SERVICE", "APP", "PENALTY_UPOKT", "PROOF_STATUS"}
	ow := NewOutputWriter(os.Stdout, qOutput, columns)
	ow.WriteHeader()

	for _, s := range results {
		supplier := s.SupplierOperatorAddress
		app := s.ApplicationAddress
		if qOutput == "table" {
			supplier = truncAddr(supplier)
			app = truncAddr(app)
		}

		ow.WriteRow([]string{
			strconv.FormatInt(s.BlockHeight, 10),
			s.BlockTimestamp.Format("2006-01-02T15:04:05Z"),
			supplier,
			s.ServiceID,
			app,
			strconv.FormatInt(s.SlashPenaltyUpokt, 10),
			strconv.FormatInt(int64(s.ClaimProofStatus), 10),
		})
	}

	ow.Flush()
	return nil
}
