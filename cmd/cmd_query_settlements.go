package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

func init() {
	queryCmd.AddCommand(querySettlementsCmd)
}

var querySettlementsCmd = &cobra.Command{
	Use:   "settlements",
	Short: "Query settlement events",
	Long: `Query all settlement events (settled, expired, slashed, discarded) from
the local database with optional filters.`,
	RunE: runQuerySettlements,
}

func runQuerySettlements(cmd *cobra.Command, _ []string) error {
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
		return fmt.Errorf("querying settlements: %w", err)
	}

	columns := []string{"HEIGHT", "TIMESTAMP", "TYPE", "SUPPLIER", "SERVICE", "APP", "CLAIMED_UPOKT", "EST_RELAYS", "OVERSERVICED"}
	ow := NewOutputWriter(os.Stdout, qOutput, columns)
	ow.WriteHeader()

	for _, s := range results {
		supplier := s.SupplierOperatorAddress
		app := s.ApplicationAddress
		if qOutput == "table" {
			supplier = truncAddr(supplier)
			app = truncAddr(app)
		}

		overserviced := "no"
		if s.IsOverserviced {
			overserviced = "yes"
		}

		ow.WriteRow([]string{
			strconv.FormatInt(s.BlockHeight, 10),
			s.BlockTimestamp.Format("2006-01-02T15:04:05Z"),
			s.EventType,
			supplier,
			s.ServiceID,
			app,
			strconv.FormatInt(s.ClaimedUpokt, 10),
			strconv.FormatInt(s.EstimatedRelays, 10),
			overserviced,
		})
	}

	ow.Flush()
	return nil
}
