package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/pokt-network/pocket-settlement-monitor/store"
)

var qPeriod string

func init() {
	queryCmd.AddCommand(querySummariesCmd)
	querySummariesCmd.Flags().StringVar(&qPeriod, "period", "daily", "Summary period: hourly, daily")
}

var querySummariesCmd = &cobra.Command{
	Use:   "summaries",
	Short: "Query hourly or daily settlement summaries",
	Long: `Query aggregated settlement summaries from the local database.
Use --period to select hourly or daily granularity (default: daily).
Use --service to filter by service ID; omit for network-wide summaries.`,
	RunE: runQuerySummaries,
}

func runQuerySummaries(cmd *cobra.Command, _ []string) error {
	if err := validateOutputFormat(); err != nil {
		return err
	}

	if qPeriod != "hourly" && qPeriod != "daily" {
		return fmt.Errorf("invalid period %q: must be 'hourly' or 'daily'", qPeriod)
	}

	db, err := openQueryStore(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	fromTime, toTime, _, _, err := parseQueryFilters()
	if err != nil {
		return err
	}

	filters := store.SummaryFilters{
		ServiceID: qService,
		FromTime:  fromTime,
		ToTime:    toTime,
		Limit:     qLimit,
	}

	columns := []string{"PERIOD", "SERVICE", "SETTLED", "EXPIRED", "SLASHED", "DISCARDED", "CLAIMED_UPOKT", "EFFECTIVE_UPOKT", "EST_RELAYS", "OVERSERVICE", "SUPPLIERS"}
	ow := NewOutputWriter(os.Stdout, qOutput, columns)
	ow.WriteHeader()

	ctx := cmd.Context()

	switch qPeriod {
	case "hourly":
		results, err := db.QueryHourlySummariesFiltered(ctx, filters)
		if err != nil {
			return fmt.Errorf("querying hourly summaries: %w", err)
		}
		for _, s := range results {
			ow.WriteRow([]string{
				s.HourStart.Format(time.RFC3339),
				s.ServiceID,
				strconv.FormatInt(s.ClaimsSettled, 10),
				strconv.FormatInt(s.ClaimsExpired, 10),
				strconv.FormatInt(s.ClaimsSlashed, 10),
				strconv.FormatInt(s.ClaimsDiscarded, 10),
				strconv.FormatInt(s.ClaimedTotalUpokt, 10),
				strconv.FormatInt(s.EffectiveTotalUpokt, 10),
				strconv.FormatInt(s.EstimatedRelays, 10),
				strconv.FormatInt(s.OverserviceCount, 10),
				strconv.FormatInt(s.ActiveSupplierCount, 10),
			})
		}

	case "daily":
		results, err := db.QueryDailySummariesFiltered(ctx, filters)
		if err != nil {
			return fmt.Errorf("querying daily summaries: %w", err)
		}
		for _, s := range results {
			ow.WriteRow([]string{
				s.DayDate.Format("2006-01-02"),
				s.ServiceID,
				strconv.FormatInt(s.ClaimsSettled, 10),
				strconv.FormatInt(s.ClaimsExpired, 10),
				strconv.FormatInt(s.ClaimsSlashed, 10),
				strconv.FormatInt(s.ClaimsDiscarded, 10),
				strconv.FormatInt(s.ClaimedTotalUpokt, 10),
				strconv.FormatInt(s.EffectiveTotalUpokt, 10),
				strconv.FormatInt(s.EstimatedRelays, 10),
				strconv.FormatInt(s.OverserviceCount, 10),
				strconv.FormatInt(s.ActiveSupplierCount, 10),
			})
		}
	}

	ow.Flush()
	return nil
}
