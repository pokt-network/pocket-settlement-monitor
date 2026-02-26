package store

import (
	"context"
	"fmt"
	"time"
)

// QuerySettlementsFiltered queries the settlements table with dynamic filtering.
// All filter fields are optional; zero-value fields are ignored.
// Results are ordered by block_height DESC, id DESC (most recent first).
func (s *SQLiteStore) QuerySettlementsFiltered(ctx context.Context, filters SettlementFilters) ([]Settlement, error) {
	query := `SELECT id, block_height, block_timestamp, event_type,
		supplier_operator_address, application_address, service_id,
		session_end_block_height, claim_proof_status,
		claimed_upokt, num_relays, num_claimed_compute_units, num_estimated_compute_units,
		proof_requirement, estimated_relays, difficulty_multiplier,
		is_overserviced, effective_burn_upokt, overservice_diff_upokt,
		expiration_reason, error_message, slash_penalty_upokt
		FROM settlements WHERE 1=1`
	var args []interface{}

	if filters.EventType != "" {
		query += " AND event_type = ?"
		args = append(args, filters.EventType)
	}
	if filters.SupplierOperatorAddress != "" {
		query += " AND supplier_operator_address = ?"
		args = append(args, filters.SupplierOperatorAddress)
	}
	if filters.ServiceID != "" {
		query += " AND service_id = ?"
		args = append(args, filters.ServiceID)
	}
	if !filters.FromTime.IsZero() {
		query += " AND block_timestamp >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND block_timestamp < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}
	if filters.FromHeight > 0 {
		query += " AND block_height >= ?"
		args = append(args, filters.FromHeight)
	}
	if filters.ToHeight > 0 {
		query += " AND block_height <= ?"
		args = append(args, filters.ToHeight)
	}

	query += " ORDER BY block_height DESC, id DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered settlements: %w", err)
	}
	defer rows.Close()

	var settlements []Settlement
	for rows.Next() {
		var st Settlement
		var blockTSStr string
		var isOverserviced int
		if err := rows.Scan(
			&st.ID, &st.BlockHeight, &blockTSStr, &st.EventType,
			&st.SupplierOperatorAddress, &st.ApplicationAddress, &st.ServiceID,
			&st.SessionEndBlockHeight, &st.ClaimProofStatus,
			&st.ClaimedUpokt, &st.NumRelays, &st.NumClaimedComputeUnits, &st.NumEstimatedComputeUnits,
			&st.ProofRequirement, &st.EstimatedRelays, &st.DifficultyMultiplier,
			&isOverserviced, &st.EffectiveBurnUpokt, &st.OverserviceDiffUpokt,
			&st.ExpirationReason, &st.ErrorMessage, &st.SlashPenaltyUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning filtered settlement row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing settlement block_timestamp %q: %w", blockTSStr, err)
		}
		st.BlockTimestamp = ts
		st.IsOverserviced = isOverserviced != 0
		settlements = append(settlements, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating filtered settlements: %w", err)
	}

	return settlements, nil
}

// QueryOverserviceFiltered queries the overservice_events table with dynamic filtering.
// All filter fields are optional; zero-value fields are ignored.
// Results are ordered by block_height DESC, id DESC (most recent first).
func (s *SQLiteStore) QueryOverserviceFiltered(ctx context.Context, filters OverserviceFilters) ([]OverserviceEvent, error) {
	query := `SELECT id, block_height, block_timestamp,
		application_address, supplier_operator_address,
		expected_burn_upokt, effective_burn_upokt
		FROM overservice_events WHERE 1=1`
	var args []interface{}

	if filters.SupplierOperatorAddress != "" {
		query += " AND supplier_operator_address = ?"
		args = append(args, filters.SupplierOperatorAddress)
	}
	if filters.ApplicationAddress != "" {
		query += " AND application_address = ?"
		args = append(args, filters.ApplicationAddress)
	}
	if !filters.FromTime.IsZero() {
		query += " AND block_timestamp >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND block_timestamp < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}
	if filters.FromHeight > 0 {
		query += " AND block_height >= ?"
		args = append(args, filters.FromHeight)
	}
	if filters.ToHeight > 0 {
		query += " AND block_height <= ?"
		args = append(args, filters.ToHeight)
	}

	query += " ORDER BY block_height DESC, id DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered overservice events: %w", err)
	}
	defer rows.Close()

	var events []OverserviceEvent
	for rows.Next() {
		var e OverserviceEvent
		var blockTSStr string
		if err := rows.Scan(
			&e.ID, &e.BlockHeight, &blockTSStr,
			&e.ApplicationAddress, &e.SupplierOperatorAddress,
			&e.ExpectedBurnUpokt, &e.EffectiveBurnUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning filtered overservice event row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing overservice block_timestamp %q: %w", blockTSStr, err)
		}
		e.BlockTimestamp = ts
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating filtered overservice events: %w", err)
	}

	return events, nil
}

// QueryReimbursementsFiltered queries the reimbursement_events table with dynamic filtering.
// All filter fields are optional; zero-value fields are ignored.
// Results are ordered by block_height DESC, id DESC (most recent first).
func (s *SQLiteStore) QueryReimbursementsFiltered(ctx context.Context, filters ReimbursementFilters) ([]ReimbursementEvent, error) {
	query := `SELECT id, block_height, block_timestamp,
		application_address, supplier_operator_address, supplier_owner_address,
		service_id, session_id, amount_upokt
		FROM reimbursement_events WHERE 1=1`
	var args []interface{}

	if filters.SupplierOperatorAddress != "" {
		query += " AND supplier_operator_address = ?"
		args = append(args, filters.SupplierOperatorAddress)
	}
	if filters.ServiceID != "" {
		query += " AND service_id = ?"
		args = append(args, filters.ServiceID)
	}
	if filters.ApplicationAddress != "" {
		query += " AND application_address = ?"
		args = append(args, filters.ApplicationAddress)
	}
	if !filters.FromTime.IsZero() {
		query += " AND block_timestamp >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND block_timestamp < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}
	if filters.FromHeight > 0 {
		query += " AND block_height >= ?"
		args = append(args, filters.FromHeight)
	}
	if filters.ToHeight > 0 {
		query += " AND block_height <= ?"
		args = append(args, filters.ToHeight)
	}

	query += " ORDER BY block_height DESC, id DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered reimbursement events: %w", err)
	}
	defer rows.Close()

	var events []ReimbursementEvent
	for rows.Next() {
		var e ReimbursementEvent
		var blockTSStr string
		if err := rows.Scan(
			&e.ID, &e.BlockHeight, &blockTSStr,
			&e.ApplicationAddress, &e.SupplierOperatorAddress, &e.SupplierOwnerAddress,
			&e.ServiceID, &e.SessionID, &e.AmountUpokt,
		); err != nil {
			return nil, fmt.Errorf("scanning filtered reimbursement event row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, blockTSStr)
		if err != nil {
			return nil, fmt.Errorf("parsing reimbursement block_timestamp %q: %w", blockTSStr, err)
		}
		e.BlockTimestamp = ts
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating filtered reimbursement events: %w", err)
	}

	return events, nil
}

// QueryHourlySummariesFiltered queries hourly summary tables with dynamic filtering.
// When ServiceID is set, queries hourly_summaries_service with optional service_id filter.
// When ServiceID is empty, queries hourly_summaries_network and maps results into
// HourlySummaryService structs with ServiceID="network".
// Results are ordered by hour_start DESC (most recent first).
func (s *SQLiteStore) QueryHourlySummariesFiltered(ctx context.Context, filters SummaryFilters) ([]HourlySummaryService, error) {
	if filters.ServiceID != "" {
		return s.queryHourlySummariesService(ctx, filters)
	}
	return s.queryHourlySummariesNetwork(ctx, filters)
}

func (s *SQLiteStore) queryHourlySummariesService(ctx context.Context, filters SummaryFilters) ([]HourlySummaryService, error) {
	query := `SELECT id, hour_start, service_id,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM hourly_summaries_service WHERE 1=1`
	var args []interface{}

	if filters.ServiceID != "" {
		query += " AND service_id = ?"
		args = append(args, filters.ServiceID)
	}
	if !filters.FromTime.IsZero() {
		query += " AND hour_start >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND hour_start < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}

	query += " ORDER BY hour_start DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered hourly service summaries: %w", err)
	}
	defer rows.Close()

	var summaries []HourlySummaryService
	for rows.Next() {
		var sm HourlySummaryService
		var hourStartStr string
		if err := rows.Scan(
			&sm.ID, &hourStartStr, &sm.ServiceID,
			&sm.ClaimsSettled, &sm.ClaimsExpired, &sm.ClaimsSlashed, &sm.ClaimsDiscarded,
			&sm.ClaimedTotalUpokt, &sm.EffectiveTotalUpokt,
			&sm.NumRelays, &sm.EstimatedRelays,
			&sm.NumComputeUnits, &sm.EstimatedComputeUnits,
			&sm.OverserviceCount, &sm.ReimbursementTotalUpokt, &sm.ActiveSupplierCount,
		); err != nil {
			return nil, fmt.Errorf("scanning hourly service summary row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, hourStartStr)
		if err != nil {
			return nil, fmt.Errorf("parsing hour_start %q: %w", hourStartStr, err)
		}
		sm.HourStart = ts
		summaries = append(summaries, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hourly service summaries: %w", err)
	}

	return summaries, nil
}

func (s *SQLiteStore) queryHourlySummariesNetwork(ctx context.Context, filters SummaryFilters) ([]HourlySummaryService, error) {
	query := `SELECT id, hour_start,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM hourly_summaries_network WHERE 1=1`
	var args []interface{}

	if !filters.FromTime.IsZero() {
		query += " AND hour_start >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND hour_start < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}

	query += " ORDER BY hour_start DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered hourly network summaries: %w", err)
	}
	defer rows.Close()

	var summaries []HourlySummaryService
	for rows.Next() {
		var sm HourlySummaryService
		var hourStartStr string
		if err := rows.Scan(
			&sm.ID, &hourStartStr,
			&sm.ClaimsSettled, &sm.ClaimsExpired, &sm.ClaimsSlashed, &sm.ClaimsDiscarded,
			&sm.ClaimedTotalUpokt, &sm.EffectiveTotalUpokt,
			&sm.NumRelays, &sm.EstimatedRelays,
			&sm.NumComputeUnits, &sm.EstimatedComputeUnits,
			&sm.OverserviceCount, &sm.ReimbursementTotalUpokt, &sm.ActiveSupplierCount,
		); err != nil {
			return nil, fmt.Errorf("scanning hourly network summary row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, hourStartStr)
		if err != nil {
			return nil, fmt.Errorf("parsing hour_start %q: %w", hourStartStr, err)
		}
		sm.HourStart = ts
		sm.ServiceID = "network"
		summaries = append(summaries, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hourly network summaries: %w", err)
	}

	return summaries, nil
}

// QueryDailySummariesFiltered queries daily summary tables with dynamic filtering.
// When ServiceID is set, queries daily_summaries_service with optional service_id filter.
// When ServiceID is empty, queries daily_summaries_network and maps results into
// DailySummaryService structs with ServiceID="network".
// Results are ordered by day_date DESC (most recent first).
func (s *SQLiteStore) QueryDailySummariesFiltered(ctx context.Context, filters SummaryFilters) ([]DailySummaryService, error) {
	if filters.ServiceID != "" {
		return s.queryDailySummariesService(ctx, filters)
	}
	return s.queryDailySummariesNetwork(ctx, filters)
}

func (s *SQLiteStore) queryDailySummariesService(ctx context.Context, filters SummaryFilters) ([]DailySummaryService, error) {
	query := `SELECT id, day_date, service_id,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM daily_summaries_service WHERE 1=1`
	var args []interface{}

	if filters.ServiceID != "" {
		query += " AND service_id = ?"
		args = append(args, filters.ServiceID)
	}
	if !filters.FromTime.IsZero() {
		query += " AND day_date >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND day_date < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}

	query += " ORDER BY day_date DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered daily service summaries: %w", err)
	}
	defer rows.Close()

	var summaries []DailySummaryService
	for rows.Next() {
		var sm DailySummaryService
		var dayDateStr string
		if err := rows.Scan(
			&sm.ID, &dayDateStr, &sm.ServiceID,
			&sm.ClaimsSettled, &sm.ClaimsExpired, &sm.ClaimsSlashed, &sm.ClaimsDiscarded,
			&sm.ClaimedTotalUpokt, &sm.EffectiveTotalUpokt,
			&sm.NumRelays, &sm.EstimatedRelays,
			&sm.NumComputeUnits, &sm.EstimatedComputeUnits,
			&sm.OverserviceCount, &sm.ReimbursementTotalUpokt, &sm.ActiveSupplierCount,
		); err != nil {
			return nil, fmt.Errorf("scanning daily service summary row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, dayDateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing day_date %q: %w", dayDateStr, err)
		}
		sm.DayDate = ts
		summaries = append(summaries, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating daily service summaries: %w", err)
	}

	return summaries, nil
}

func (s *SQLiteStore) queryDailySummariesNetwork(ctx context.Context, filters SummaryFilters) ([]DailySummaryService, error) {
	query := `SELECT id, day_date,
		claims_settled, claims_expired, claims_slashed, claims_discarded,
		claimed_total_upokt, effective_total_upokt, num_relays, estimated_relays,
		num_compute_units, estimated_compute_units, overservice_count,
		reimbursement_total_upokt, active_supplier_count
		FROM daily_summaries_network WHERE 1=1`
	var args []interface{}

	if !filters.FromTime.IsZero() {
		query += " AND day_date >= ?"
		args = append(args, filters.FromTime.Format(time.RFC3339))
	}
	if !filters.ToTime.IsZero() {
		query += " AND day_date < ?"
		args = append(args, filters.ToTime.Format(time.RFC3339))
	}

	query += " ORDER BY day_date DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filters.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying filtered daily network summaries: %w", err)
	}
	defer rows.Close()

	var summaries []DailySummaryService
	for rows.Next() {
		var sm DailySummaryService
		var dayDateStr string
		if err := rows.Scan(
			&sm.ID, &dayDateStr,
			&sm.ClaimsSettled, &sm.ClaimsExpired, &sm.ClaimsSlashed, &sm.ClaimsDiscarded,
			&sm.ClaimedTotalUpokt, &sm.EffectiveTotalUpokt,
			&sm.NumRelays, &sm.EstimatedRelays,
			&sm.NumComputeUnits, &sm.EstimatedComputeUnits,
			&sm.OverserviceCount, &sm.ReimbursementTotalUpokt, &sm.ActiveSupplierCount,
		); err != nil {
			return nil, fmt.Errorf("scanning daily network summary row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339, dayDateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing day_date %q: %w", dayDateStr, err)
		}
		sm.DayDate = ts
		sm.ServiceID = "network"
		summaries = append(summaries, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating daily network summaries: %w", err)
	}

	return summaries, nil
}
