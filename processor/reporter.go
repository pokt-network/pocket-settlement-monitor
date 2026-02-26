package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/pokt-network/pocket-settlement-monitor/logging"
	"github.com/pokt-network/pocket-settlement-monitor/store"
)

// Reporter implements BlockReporter with DB-based read-accumulate-write for
// all 4 summary tables (hourly/daily x service/network). It tracks known
// service IDs to generate zero-value rows for services with no events in a block.
type Reporter struct {
	store           store.Store
	logger          zerolog.Logger
	knownServiceIDs map[string]struct{}
}

// NewReporter creates a Reporter, initializing knownServiceIDs from existing
// settlements in the database.
func NewReporter(ctx context.Context, s store.Store, logger zerolog.Logger) (*Reporter, error) {
	ids, err := s.DistinctServiceIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("initializing known service IDs: %w", err)
	}

	known := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		known[id] = struct{}{}
	}

	return &Reporter{
		store:           s,
		logger:          logging.ForComponent(logger, "reporter"),
		knownServiceIDs: known,
	}, nil
}

// hourStart returns the start of the hour containing ts (UTC).
func hourStart(ts time.Time) time.Time {
	return ts.UTC().Truncate(time.Hour)
}

// dayStart returns the start of the day containing ts (UTC).
func dayStart(ts time.Time) time.Time {
	u := ts.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// hourEnd returns the start of the next hour after ts.
func hourEnd(ts time.Time) time.Time {
	return hourStart(ts).Add(time.Hour)
}

// dayEnd returns the start of the next day after ts.
func dayEnd(ts time.Time) time.Time {
	return dayStart(ts).AddDate(0, 0, 1)
}

// blockContribution holds aggregated values from a single block's events.
type blockContribution struct {
	claimsSettled         int64
	claimsExpired         int64
	claimsSlashed         int64
	claimsDiscarded       int64
	claimedUpokt          int64
	effectiveUpokt        int64
	numRelays             int64
	estimatedRelays       int64
	numComputeUnits       int64
	estimatedComputeUnits int64
	overserviceCount      int64
	reimbursementUpokt    int64
}

// aggregateBlock computes a blockContribution from settlements, overservices, and reimbursements.
func aggregateBlock(settlements []store.Settlement, overservices []store.OverserviceEvent, reimbursements []store.ReimbursementEvent) blockContribution {
	var bc blockContribution
	for _, s := range settlements {
		switch s.EventType {
		case "settled":
			bc.claimsSettled++
			bc.claimedUpokt += s.ClaimedUpokt
			if s.IsOverserviced {
				bc.effectiveUpokt += s.EffectiveBurnUpokt
			} else {
				bc.effectiveUpokt += s.ClaimedUpokt
			}
			bc.numRelays += s.NumRelays
			bc.estimatedRelays += s.EstimatedRelays
			bc.numComputeUnits += s.NumClaimedComputeUnits
			bc.estimatedComputeUnits += s.NumEstimatedComputeUnits
		case "expired":
			bc.claimsExpired++
			bc.claimedUpokt += s.ClaimedUpokt
			bc.effectiveUpokt += s.ClaimedUpokt
			bc.numRelays += s.NumRelays
			bc.estimatedRelays += s.EstimatedRelays
			bc.numComputeUnits += s.NumClaimedComputeUnits
			bc.estimatedComputeUnits += s.NumEstimatedComputeUnits
		case "slashed":
			bc.claimsSlashed++
		case "discarded":
			bc.claimsDiscarded++
		}
	}
	bc.overserviceCount = int64(len(overservices))
	for _, r := range reimbursements {
		bc.reimbursementUpokt += r.AmountUpokt
	}
	return bc
}

// groupSettlementsByService groups settlements by ServiceID.
func groupSettlementsByService(settlements []store.Settlement) map[string][]store.Settlement {
	groups := make(map[string][]store.Settlement)
	for _, s := range settlements {
		groups[s.ServiceID] = append(groups[s.ServiceID], s)
	}
	return groups
}

// groupReimbursementsByService groups reimbursement events by ServiceID.
func groupReimbursementsByService(reimbursements []store.ReimbursementEvent) map[string][]store.ReimbursementEvent {
	groups := make(map[string][]store.ReimbursementEvent)
	for _, r := range reimbursements {
		groups[r.ServiceID] = append(groups[r.ServiceID], r)
	}
	return groups
}

// ReportBlock materializes hourly and daily summary rows for all 4 summary tables
// using DB-based read-accumulate-write. Zero-value rows are created for all known
// service IDs. ActiveSupplierCount is recomputed from DB on each call.
func (r *Reporter) ReportBlock(ctx context.Context, height int64, ts time.Time,
	settlements []store.Settlement,
	overservices []store.OverserviceEvent,
	reimbursements []store.ReimbursementEvent) error {

	// Compute period boundaries.
	hs := hourStart(ts)
	he := hourEnd(ts)
	ds := dayStart(ts)
	de := dayEnd(ts)

	// Update known service IDs from this block's settlements.
	for _, s := range settlements {
		if s.ServiceID != "" {
			r.knownServiceIDs[s.ServiceID] = struct{}{}
		}
	}

	// Group by service.
	settlementsByService := groupSettlementsByService(settlements)
	reimbursementsByService := groupReimbursementsByService(reimbursements)

	// 1. Hourly per-service summaries.
	for serviceID := range r.knownServiceIDs {
		svcSettlements := settlementsByService[serviceID]
		svcReimbursements := reimbursementsByService[serviceID]
		// Per-service doesn't track overservice (events lack service_id).
		bc := aggregateBlock(svcSettlements, nil, svcReimbursements)

		current, err := r.store.GetHourlySummaryService(ctx, hs, serviceID)
		if err != nil {
			return fmt.Errorf("reading hourly service summary for %s: %w", serviceID, err)
		}

		supplierCount, err := r.store.CountActiveSuppliers(ctx, hs, he, serviceID)
		if err != nil {
			return fmt.Errorf("counting active suppliers (hourly, service %s): %w", serviceID, err)
		}

		summary := store.HourlySummaryService{
			HourStart:               hs,
			ServiceID:               serviceID,
			ClaimsSettled:           current.ClaimsSettled + bc.claimsSettled,
			ClaimsExpired:           current.ClaimsExpired + bc.claimsExpired,
			ClaimsSlashed:           current.ClaimsSlashed + bc.claimsSlashed,
			ClaimsDiscarded:         current.ClaimsDiscarded + bc.claimsDiscarded,
			ClaimedTotalUpokt:       current.ClaimedTotalUpokt + bc.claimedUpokt,
			EffectiveTotalUpokt:     current.EffectiveTotalUpokt + bc.effectiveUpokt,
			NumRelays:               current.NumRelays + bc.numRelays,
			EstimatedRelays:         current.EstimatedRelays + bc.estimatedRelays,
			NumComputeUnits:         current.NumComputeUnits + bc.numComputeUnits,
			EstimatedComputeUnits:   current.EstimatedComputeUnits + bc.estimatedComputeUnits,
			OverserviceCount:        current.OverserviceCount + bc.overserviceCount,
			ReimbursementTotalUpokt: current.ReimbursementTotalUpokt + bc.reimbursementUpokt,
			ActiveSupplierCount:     supplierCount,
		}

		if err := r.store.UpsertHourlySummaryService(ctx, summary); err != nil {
			return fmt.Errorf("upserting hourly service summary for %s: %w", serviceID, err)
		}

		r.logger.Debug().
			Time("hour_start", hs).
			Str("service_id", serviceID).
			Int64("height", height).
			Msg("materialized hourly service summary")
	}

	// 2. Hourly network summary.
	{
		bc := aggregateBlock(settlements, overservices, reimbursements)

		current, err := r.store.GetHourlySummaryNetwork(ctx, hs)
		if err != nil {
			return fmt.Errorf("reading hourly network summary: %w", err)
		}

		supplierCount, err := r.store.CountActiveSuppliers(ctx, hs, he, "")
		if err != nil {
			return fmt.Errorf("counting active suppliers (hourly, network): %w", err)
		}

		summary := store.HourlySummaryNetwork{
			HourStart:               hs,
			ClaimsSettled:           current.ClaimsSettled + bc.claimsSettled,
			ClaimsExpired:           current.ClaimsExpired + bc.claimsExpired,
			ClaimsSlashed:           current.ClaimsSlashed + bc.claimsSlashed,
			ClaimsDiscarded:         current.ClaimsDiscarded + bc.claimsDiscarded,
			ClaimedTotalUpokt:       current.ClaimedTotalUpokt + bc.claimedUpokt,
			EffectiveTotalUpokt:     current.EffectiveTotalUpokt + bc.effectiveUpokt,
			NumRelays:               current.NumRelays + bc.numRelays,
			EstimatedRelays:         current.EstimatedRelays + bc.estimatedRelays,
			NumComputeUnits:         current.NumComputeUnits + bc.numComputeUnits,
			EstimatedComputeUnits:   current.EstimatedComputeUnits + bc.estimatedComputeUnits,
			OverserviceCount:        current.OverserviceCount + bc.overserviceCount,
			ReimbursementTotalUpokt: current.ReimbursementTotalUpokt + bc.reimbursementUpokt,
			ActiveSupplierCount:     supplierCount,
		}

		if err := r.store.UpsertHourlySummaryNetwork(ctx, summary); err != nil {
			return fmt.Errorf("upserting hourly network summary: %w", err)
		}

		r.logger.Debug().
			Time("hour_start", hs).
			Int64("height", height).
			Msg("materialized hourly network summary")
	}

	// 3. Daily per-service summaries.
	for serviceID := range r.knownServiceIDs {
		svcSettlements := settlementsByService[serviceID]
		svcReimbursements := reimbursementsByService[serviceID]
		bc := aggregateBlock(svcSettlements, nil, svcReimbursements)

		current, err := r.store.GetDailySummaryService(ctx, ds, serviceID)
		if err != nil {
			return fmt.Errorf("reading daily service summary for %s: %w", serviceID, err)
		}

		supplierCount, err := r.store.CountActiveSuppliers(ctx, ds, de, serviceID)
		if err != nil {
			return fmt.Errorf("counting active suppliers (daily, service %s): %w", serviceID, err)
		}

		summary := store.DailySummaryService{
			DayDate:                 ds,
			ServiceID:               serviceID,
			ClaimsSettled:           current.ClaimsSettled + bc.claimsSettled,
			ClaimsExpired:           current.ClaimsExpired + bc.claimsExpired,
			ClaimsSlashed:           current.ClaimsSlashed + bc.claimsSlashed,
			ClaimsDiscarded:         current.ClaimsDiscarded + bc.claimsDiscarded,
			ClaimedTotalUpokt:       current.ClaimedTotalUpokt + bc.claimedUpokt,
			EffectiveTotalUpokt:     current.EffectiveTotalUpokt + bc.effectiveUpokt,
			NumRelays:               current.NumRelays + bc.numRelays,
			EstimatedRelays:         current.EstimatedRelays + bc.estimatedRelays,
			NumComputeUnits:         current.NumComputeUnits + bc.numComputeUnits,
			EstimatedComputeUnits:   current.EstimatedComputeUnits + bc.estimatedComputeUnits,
			OverserviceCount:        current.OverserviceCount + bc.overserviceCount,
			ReimbursementTotalUpokt: current.ReimbursementTotalUpokt + bc.reimbursementUpokt,
			ActiveSupplierCount:     supplierCount,
		}

		if err := r.store.UpsertDailySummaryService(ctx, summary); err != nil {
			return fmt.Errorf("upserting daily service summary for %s: %w", serviceID, err)
		}

		r.logger.Debug().
			Time("day_start", ds).
			Str("service_id", serviceID).
			Int64("height", height).
			Msg("materialized daily service summary")
	}

	// 4. Daily network summary.
	{
		bc := aggregateBlock(settlements, overservices, reimbursements)

		current, err := r.store.GetDailySummaryNetwork(ctx, ds)
		if err != nil {
			return fmt.Errorf("reading daily network summary: %w", err)
		}

		supplierCount, err := r.store.CountActiveSuppliers(ctx, ds, de, "")
		if err != nil {
			return fmt.Errorf("counting active suppliers (daily, network): %w", err)
		}

		summary := store.DailySummaryNetwork{
			DayDate:                 ds,
			ClaimsSettled:           current.ClaimsSettled + bc.claimsSettled,
			ClaimsExpired:           current.ClaimsExpired + bc.claimsExpired,
			ClaimsSlashed:           current.ClaimsSlashed + bc.claimsSlashed,
			ClaimsDiscarded:         current.ClaimsDiscarded + bc.claimsDiscarded,
			ClaimedTotalUpokt:       current.ClaimedTotalUpokt + bc.claimedUpokt,
			EffectiveTotalUpokt:     current.EffectiveTotalUpokt + bc.effectiveUpokt,
			NumRelays:               current.NumRelays + bc.numRelays,
			EstimatedRelays:         current.EstimatedRelays + bc.estimatedRelays,
			NumComputeUnits:         current.NumComputeUnits + bc.numComputeUnits,
			EstimatedComputeUnits:   current.EstimatedComputeUnits + bc.estimatedComputeUnits,
			OverserviceCount:        current.OverserviceCount + bc.overserviceCount,
			ReimbursementTotalUpokt: current.ReimbursementTotalUpokt + bc.reimbursementUpokt,
			ActiveSupplierCount:     supplierCount,
		}

		if err := r.store.UpsertDailySummaryNetwork(ctx, summary); err != nil {
			return fmt.Errorf("upserting daily network summary: %w", err)
		}

		r.logger.Debug().
			Time("day_start", ds).
			Int64("height", height).
			Msg("materialized daily network summary")
	}

	return nil
}

// RecalculateSummariesForRange rebuilds all summaries from raw events for the
// given time range. This is a FULL REPLACE (not accumulate) -- existing summary
// rows are overwritten. Called after backfill completes.
//
// The range is expanded to full day boundaries to ensure that daily summaries
// are always recalculated from ALL events for each affected day, not just the
// narrow backfill window. Without this, a partial-day backfill would overwrite
// the daily aggregate with only its own events.
func (r *Reporter) RecalculateSummariesForRange(ctx context.Context, from, to time.Time) error {
	// Expand to full day boundaries so daily summaries include all events for
	// each touched day. Example: if from=02:26 and to=03:26, expand to
	// [00:00, 00:00+1day) to capture events at 01:09 on the same day.
	from = dayStart(from)
	to = dayEnd(to)

	// Query raw data.
	settlements, err := r.store.QuerySettlementsForPeriod(ctx, from, to)
	if err != nil {
		return fmt.Errorf("querying settlements for recalculation: %w", err)
	}
	overservices, err := r.store.QueryOverserviceEventsForPeriod(ctx, from, to)
	if err != nil {
		return fmt.Errorf("querying overservice events for recalculation: %w", err)
	}
	reimbursements, err := r.store.QueryReimbursementEventsForPeriod(ctx, from, to)
	if err != nil {
		return fmt.Errorf("querying reimbursement events for recalculation: %w", err)
	}

	// Update known service IDs from queried settlements.
	for _, s := range settlements {
		if s.ServiceID != "" {
			r.knownServiceIDs[s.ServiceID] = struct{}{}
		}
	}

	// Group settlements by hour.
	type hourServiceKey struct {
		hour      time.Time
		serviceID string
	}
	settlementsByHourService := make(map[hourServiceKey][]store.Settlement)
	settlementsByHour := make(map[time.Time][]store.Settlement)
	distinctHours := make(map[time.Time]struct{})

	for _, s := range settlements {
		h := hourStart(s.BlockTimestamp)
		distinctHours[h] = struct{}{}
		settlementsByHour[h] = append(settlementsByHour[h], s)
		key := hourServiceKey{hour: h, serviceID: s.ServiceID}
		settlementsByHourService[key] = append(settlementsByHourService[key], s)
	}

	// Group overservices by hour.
	overservicesByHour := make(map[time.Time][]store.OverserviceEvent)
	for _, o := range overservices {
		h := hourStart(o.BlockTimestamp)
		distinctHours[h] = struct{}{}
		overservicesByHour[h] = append(overservicesByHour[h], o)
	}

	// Group reimbursements by hour and service.
	type hourReimbKey struct {
		hour      time.Time
		serviceID string
	}
	reimbursementsByHourService := make(map[hourReimbKey][]store.ReimbursementEvent)
	reimbursementsByHour := make(map[time.Time][]store.ReimbursementEvent)
	for _, re := range reimbursements {
		h := hourStart(re.BlockTimestamp)
		distinctHours[h] = struct{}{}
		reimbursementsByHour[h] = append(reimbursementsByHour[h], re)
		key := hourReimbKey{hour: h, serviceID: re.ServiceID}
		reimbursementsByHourService[key] = append(reimbursementsByHourService[key], re)
	}

	hoursUpdated := 0

	// Process each hour.
	for h := range distinctHours {
		he := h.Add(time.Hour)

		// Per-service hourly summaries.
		for serviceID := range r.knownServiceIDs {
			key := hourServiceKey{hour: h, serviceID: serviceID}
			svcSettlements := settlementsByHourService[key]
			rKey := hourReimbKey{hour: h, serviceID: serviceID}
			svcReimbursements := reimbursementsByHourService[rKey]
			bc := aggregateBlock(svcSettlements, nil, svcReimbursements)

			supplierCount, err := r.store.CountActiveSuppliers(ctx, h, he, serviceID)
			if err != nil {
				return fmt.Errorf("counting active suppliers during recalculation: %w", err)
			}

			summary := store.HourlySummaryService{
				HourStart:               h,
				ServiceID:               serviceID,
				ClaimsSettled:           bc.claimsSettled,
				ClaimsExpired:           bc.claimsExpired,
				ClaimsSlashed:           bc.claimsSlashed,
				ClaimsDiscarded:         bc.claimsDiscarded,
				ClaimedTotalUpokt:       bc.claimedUpokt,
				EffectiveTotalUpokt:     bc.effectiveUpokt,
				NumRelays:               bc.numRelays,
				EstimatedRelays:         bc.estimatedRelays,
				NumComputeUnits:         bc.numComputeUnits,
				EstimatedComputeUnits:   bc.estimatedComputeUnits,
				OverserviceCount:        bc.overserviceCount,
				ReimbursementTotalUpokt: bc.reimbursementUpokt,
				ActiveSupplierCount:     supplierCount,
			}

			if err := r.store.UpsertHourlySummaryService(ctx, summary); err != nil {
				return fmt.Errorf("upserting hourly service summary during recalculation: %w", err)
			}
		}

		// Network hourly summary.
		{
			hourSettlements := settlementsByHour[h]
			hourOverservices := overservicesByHour[h]
			hourReimb := reimbursementsByHour[h]
			bc := aggregateBlock(hourSettlements, hourOverservices, hourReimb)

			supplierCount, err := r.store.CountActiveSuppliers(ctx, h, he, "")
			if err != nil {
				return fmt.Errorf("counting active suppliers (network) during recalculation: %w", err)
			}

			summary := store.HourlySummaryNetwork{
				HourStart:               h,
				ClaimsSettled:           bc.claimsSettled,
				ClaimsExpired:           bc.claimsExpired,
				ClaimsSlashed:           bc.claimsSlashed,
				ClaimsDiscarded:         bc.claimsDiscarded,
				ClaimedTotalUpokt:       bc.claimedUpokt,
				EffectiveTotalUpokt:     bc.effectiveUpokt,
				NumRelays:               bc.numRelays,
				EstimatedRelays:         bc.estimatedRelays,
				NumComputeUnits:         bc.numComputeUnits,
				EstimatedComputeUnits:   bc.estimatedComputeUnits,
				OverserviceCount:        bc.overserviceCount,
				ReimbursementTotalUpokt: bc.reimbursementUpokt,
				ActiveSupplierCount:     supplierCount,
			}

			if err := r.store.UpsertHourlySummaryNetwork(ctx, summary); err != nil {
				return fmt.Errorf("upserting hourly network summary during recalculation: %w", err)
			}
		}

		hoursUpdated++
	}

	// Group by day for daily summaries.
	type dayServiceKey struct {
		day       time.Time
		serviceID string
	}
	settlementsByDayService := make(map[dayServiceKey][]store.Settlement)
	settlementsByDay := make(map[time.Time][]store.Settlement)
	distinctDays := make(map[time.Time]struct{})

	for _, s := range settlements {
		d := dayStart(s.BlockTimestamp)
		distinctDays[d] = struct{}{}
		settlementsByDay[d] = append(settlementsByDay[d], s)
		key := dayServiceKey{day: d, serviceID: s.ServiceID}
		settlementsByDayService[key] = append(settlementsByDayService[key], s)
	}

	overservicesByDay := make(map[time.Time][]store.OverserviceEvent)
	for _, o := range overservices {
		d := dayStart(o.BlockTimestamp)
		distinctDays[d] = struct{}{}
		overservicesByDay[d] = append(overservicesByDay[d], o)
	}

	type dayReimbKey struct {
		day       time.Time
		serviceID string
	}
	reimbursementsByDayService := make(map[dayReimbKey][]store.ReimbursementEvent)
	reimbursementsByDay := make(map[time.Time][]store.ReimbursementEvent)
	for _, re := range reimbursements {
		d := dayStart(re.BlockTimestamp)
		distinctDays[d] = struct{}{}
		reimbursementsByDay[d] = append(reimbursementsByDay[d], re)
		key := dayReimbKey{day: d, serviceID: re.ServiceID}
		reimbursementsByDayService[key] = append(reimbursementsByDayService[key], re)
	}

	daysUpdated := 0

	for d := range distinctDays {
		de := d.AddDate(0, 0, 1)

		// Per-service daily summaries.
		for serviceID := range r.knownServiceIDs {
			key := dayServiceKey{day: d, serviceID: serviceID}
			svcSettlements := settlementsByDayService[key]
			rKey := dayReimbKey{day: d, serviceID: serviceID}
			svcReimbursements := reimbursementsByDayService[rKey]
			bc := aggregateBlock(svcSettlements, nil, svcReimbursements)

			supplierCount, err := r.store.CountActiveSuppliers(ctx, d, de, serviceID)
			if err != nil {
				return fmt.Errorf("counting active suppliers (daily, service) during recalculation: %w", err)
			}

			summary := store.DailySummaryService{
				DayDate:                 d,
				ServiceID:               serviceID,
				ClaimsSettled:           bc.claimsSettled,
				ClaimsExpired:           bc.claimsExpired,
				ClaimsSlashed:           bc.claimsSlashed,
				ClaimsDiscarded:         bc.claimsDiscarded,
				ClaimedTotalUpokt:       bc.claimedUpokt,
				EffectiveTotalUpokt:     bc.effectiveUpokt,
				NumRelays:               bc.numRelays,
				EstimatedRelays:         bc.estimatedRelays,
				NumComputeUnits:         bc.numComputeUnits,
				EstimatedComputeUnits:   bc.estimatedComputeUnits,
				OverserviceCount:        bc.overserviceCount,
				ReimbursementTotalUpokt: bc.reimbursementUpokt,
				ActiveSupplierCount:     supplierCount,
			}

			if err := r.store.UpsertDailySummaryService(ctx, summary); err != nil {
				return fmt.Errorf("upserting daily service summary during recalculation: %w", err)
			}
		}

		// Network daily summary.
		{
			daySettlements := settlementsByDay[d]
			dayOverservices := overservicesByDay[d]
			dayReimb := reimbursementsByDay[d]
			bc := aggregateBlock(daySettlements, dayOverservices, dayReimb)

			supplierCount, err := r.store.CountActiveSuppliers(ctx, d, de, "")
			if err != nil {
				return fmt.Errorf("counting active suppliers (daily, network) during recalculation: %w", err)
			}

			summary := store.DailySummaryNetwork{
				DayDate:                 d,
				ClaimsSettled:           bc.claimsSettled,
				ClaimsExpired:           bc.claimsExpired,
				ClaimsSlashed:           bc.claimsSlashed,
				ClaimsDiscarded:         bc.claimsDiscarded,
				ClaimedTotalUpokt:       bc.claimedUpokt,
				EffectiveTotalUpokt:     bc.effectiveUpokt,
				NumRelays:               bc.numRelays,
				EstimatedRelays:         bc.estimatedRelays,
				NumComputeUnits:         bc.numComputeUnits,
				EstimatedComputeUnits:   bc.estimatedComputeUnits,
				OverserviceCount:        bc.overserviceCount,
				ReimbursementTotalUpokt: bc.reimbursementUpokt,
				ActiveSupplierCount:     supplierCount,
			}

			if err := r.store.UpsertDailySummaryNetwork(ctx, summary); err != nil {
				return fmt.Errorf("upserting daily network summary during recalculation: %w", err)
			}
		}

		daysUpdated++
	}

	r.logger.Info().
		Time("from", from).
		Time("to", to).
		Int("hours_updated", hoursUpdated).
		Int("days_updated", daysUpdated).
		Msg("recalculated summaries for range")

	return nil
}
