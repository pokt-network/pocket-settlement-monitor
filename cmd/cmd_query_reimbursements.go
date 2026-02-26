package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

func init() {
	queryCmd.AddCommand(queryReimbursementsCmd)
}

var queryReimbursementsCmd = &cobra.Command{
	Use:   "reimbursements",
	Short: "Query reimbursement request events",
	Long: `Query application reimbursement request events from the local database.
Shows the amount, session, and involved parties for each reimbursement.`,
	RunE: runQueryReimbursements,
}

func runQueryReimbursements(cmd *cobra.Command, _ []string) error {
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

	filters := store.ReimbursementFilters{
		SupplierOperatorAddress: qSupplier,
		ServiceID:               qService,
		FromTime:                fromTime,
		ToTime:                  toTime,
		FromHeight:              fromHeight,
		ToHeight:                toHeight,
		Limit:                   qLimit,
	}

	ctx := cmd.Context()
	results, err := db.QueryReimbursementsFiltered(ctx, filters)
	if err != nil {
		return fmt.Errorf("querying reimbursements: %w", err)
	}

	columns := []string{"HEIGHT", "TIMESTAMP", "APPLICATION", "SUPPLIER", "SERVICE", "SESSION", "AMOUNT_UPOKT"}
	ow := NewOutputWriter(os.Stdout, qOutput, columns)
	ow.WriteHeader()

	for _, e := range results {
		app := e.ApplicationAddress
		supplier := e.SupplierOperatorAddress
		session := e.SessionID
		if qOutput == "table" {
			app = truncAddr(app)
			supplier = truncAddr(supplier)
			session = truncAddr(session)
		}

		ow.WriteRow([]string{
			strconv.FormatInt(e.BlockHeight, 10),
			e.BlockTimestamp.Format("2006-01-02T15:04:05Z"),
			app,
			supplier,
			e.ServiceID,
			session,
			strconv.FormatInt(e.AmountUpokt, 10),
		})
	}

	ow.Flush()
	return nil
}
