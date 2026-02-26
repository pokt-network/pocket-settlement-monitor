package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

// parseFlexibleTime parses a string as a time value, trying multiple formats.
// Supported formats (tried in order):
//   - RFC3339: "2006-01-02T15:04:05Z07:00"
//   - Datetime without timezone (assumes UTC): "2006-01-02T15:04:05"
//   - Date only (start of day UTC): "2006-01-02"
func parseFlexibleTime(input string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		t, err := time.Parse(format, input)
		if err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf(
		"cannot parse %q as time; accepted formats: RFC3339 (%s), datetime (%s), date (%s)",
		input, "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02",
	)
}

// resolveToHeight resolves an input string to a block height.
// If the input is a valid integer, it is returned directly as a block height.
// If the input is a date/time string, a binary search over CometBFT Header RPC
// calls is performed to find the first block at or after the target time.
// This costs ~20 RPC calls for a 1M-block chain (log2).
func resolveToHeight(ctx context.Context, client *rpchttp.HTTP, input string) (int64, error) {
	// Try parsing as integer height first.
	if h, err := strconv.ParseInt(input, 10, 64); err == nil {
		return h, nil
	}

	// Try parsing as date/time.
	targetTime, err := parseFlexibleTime(input)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as height or date: %w", input, err)
	}

	// Get current chain height for binary search upper bound.
	status, err := client.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("querying chain status for height resolution: %w", err)
	}
	currentHeight := status.SyncInfo.LatestBlockHeight

	// Binary search: find first block at or after targetTime.
	low := int64(1)
	high := currentHeight

	for low < high {
		mid := (low + high) / 2

		headerResult, err := client.Header(ctx, &mid)
		if err != nil {
			return 0, fmt.Errorf("querying header at height %d during binary search: %w", mid, err)
		}

		if headerResult.Header.Time.Before(targetTime) {
			low = mid + 1
		} else {
			high = mid
		}
	}

	return low, nil
}
