package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

func init() {
	queryCmd.AddCommand(queryOverserviceCmd)
}

var queryOverserviceCmd = &cobra.Command{
	Use:   "overservice",
	Short: "Query overservice events",
	Long: `Query application overservice events from the local database.
Shows expected vs effective burn amounts and their difference.`,
	RunE: runQueryOverservice,
}

func runQueryOverservice(cmd *cobra.Command, _ []string) error {
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

	filters := store.OverserviceFilters{
		SupplierOperatorAddress: qSupplier,
		FromTime:                fromTime,
		ToTime:                  toTime,
		FromHeight:              fromHeight,
		ToHeight:                toHeight,
		Limit:                   qLimit,
	}

	ctx := cmd.Context()
	results, err := db.QueryOverserviceFiltered(ctx, filters)
	if err != nil {
		return fmt.Errorf("querying overservice events: %w", err)
	}

	columns := []string{"HEIGHT", "TIMESTAMP", "APPLICATION", "SUPPLIER", "EXPECTED_UPOKT", "EFFECTIVE_UPOKT", "DIFF"}
	ow := NewOutputWriter(os.Stdout, qOutput, columns)
	ow.WriteHeader()

	for _, e := range results {
		app := e.ApplicationAddress
		supplier := e.SupplierOperatorAddress
		if qOutput == "table" {
			app = truncAddr(app)
			supplier = truncAddr(supplier)
		}

		diff := e.ExpectedBurnUpokt - e.EffectiveBurnUpokt

		ow.WriteRow([]string{
			strconv.FormatInt(e.BlockHeight, 10),
			e.BlockTimestamp.Format("2006-01-02T15:04:05Z"),
			app,
			supplier,
			strconv.FormatInt(e.ExpectedBurnUpokt, 10),
			strconv.FormatInt(e.EffectiveBurnUpokt, 10),
			strconv.FormatInt(diff, 10),
		})
	}

	ow.Flush()
	return nil
}
