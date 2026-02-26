package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

// parseFlexibleTime parses a string as a time value, trying multiple formats.
// Supported formats (tried in order):
//   - RFC3339: "2006-01-02T15:04:05Z07:00"
//   - Datetime without timezone (assumes UTC): "2006-01-02T15:04:05"
//   - Date only (start of day UTC): "2006-01-02"
//
// Returns the parsed time and whether the input was date-only.
func parseFlexibleTime(input string) (time.Time, bool, error) {
	// Try date-only first (most specific check).
	t, err := time.Parse("2006-01-02", input)
	if err == nil {
		return t.UTC(), true, nil
	}

	// Try RFC3339 and datetime.
	for _, format := range []string{time.RFC3339, "2006-01-02T15:04:05"} {
		t, err := time.Parse(format, input)
		if err == nil {
			return t.UTC(), false, nil
		}
	}

	return time.Time{}, false, fmt.Errorf(
		"cannot parse %q as time; accepted formats: RFC3339 (%s), datetime (%s), date (%s)",
		input, "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02",
	)
}

// lowestHeightRe extracts the actual lowest available height from pruned-node
// error messages like: "height 500 is not available, lowest height is 614401".
var lowestHeightRe = regexp.MustCompile(`lowest height is (\d+)`)

// getChainBounds returns the earliest and latest available heights from the node.
// On pruned nodes where EarliestBlockHeight is unreported (0), it probes the
// node to discover the actual lowest available height.
func getChainBounds(ctx context.Context, client *rpchttp.HTTP) (earliest, latest int64, err error) {
	status, err := client.Status(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("querying chain status: %w", err)
	}
	latest = status.SyncInfo.LatestBlockHeight
	earliest = status.SyncInfo.EarliestBlockHeight

	if earliest > 0 {
		return earliest, latest, nil
	}

	// EarliestBlockHeight is 0 — common on pruned nodes. Probe height 1 to
	// trigger an error that reveals the actual lowest available height.
	one := int64(1)
	_, probeErr := client.Header(ctx, &one)
	if probeErr != nil {
		if m := lowestHeightRe.FindStringSubmatch(probeErr.Error()); len(m) == 2 {
			parsed, parseErr := strconv.ParseInt(m[1], 10, 64)
			if parseErr == nil {
				return parsed, latest, nil
			}
		}
	}

	// Probe succeeded (height 1 is available) or error didn't contain the
	// expected pattern — fall back to 1.
	return 1, latest, nil
}

// findFirstBlockAtOrAfter binary-searches for the first block with timestamp >= target.
// If a height is unavailable (pruned node), the search adjusts bounds automatically.
func findFirstBlockAtOrAfter(ctx context.Context, client *rpchttp.HTTP, target time.Time, low, high int64) (int64, error) {
	for low < high {
		mid := (low + high) / 2

		headerResult, err := client.Header(ctx, &mid)
		if err != nil {
			// On pruned nodes, unavailable heights return an error containing
			// the actual lowest height. Adjust and continue the search.
			if m := lowestHeightRe.FindStringSubmatch(err.Error()); len(m) == 2 {
				parsed, parseErr := strconv.ParseInt(m[1], 10, 64)
				if parseErr == nil && parsed > low {
					low = parsed
					continue
				}
			}
			return 0, fmt.Errorf("querying header at height %d during binary search: %w", mid, err)
		}

		if headerResult.Header.Time.Before(target) {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low, nil
}

// resolveFromHeight resolves a --from flag to a block height.
// For integers: returned directly.
// For dates/times: finds the first block at or after the target time.
// For date-only (e.g. "2026-01-15"): first block on that day (>= 00:00:00 UTC).
func resolveFromHeight(ctx context.Context, client *rpchttp.HTTP, input string) (int64, error) {
	if h, err := strconv.ParseInt(input, 10, 64); err == nil {
		return h, nil
	}

	targetTime, _, err := parseFlexibleTime(input)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as height or date: %w", input, err)
	}

	earliest, latest, err := getChainBounds(ctx, client)
	if err != nil {
		return 0, err
	}

	return findFirstBlockAtOrAfter(ctx, client, targetTime, earliest, latest)
}

// resolveToHeight resolves a --to flag to a block height.
// For integers: returned directly.
// For datetime (e.g. "2026-01-15T14:30:00"): first block at or after that exact time.
// For date-only (e.g. "2026-01-15"): last block of that day — resolved as
// (first block on next day) - 1, so the range includes the entire day.
func resolveToHeight(ctx context.Context, client *rpchttp.HTTP, input string) (int64, error) {
	if h, err := strconv.ParseInt(input, 10, 64); err == nil {
		return h, nil
	}

	targetTime, dateOnly, err := parseFlexibleTime(input)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as height or date: %w", input, err)
	}

	earliest, latest, err := getChainBounds(ctx, client)
	if err != nil {
		return 0, err
	}

	if dateOnly {
		// Push to start of next day, find first block there, subtract 1.
		nextDay := targetTime.AddDate(0, 0, 1)
		firstOfNextDay, err := findFirstBlockAtOrAfter(ctx, client, nextDay, earliest, latest)
		if err != nil {
			return 0, err
		}
		// If the first block of next day is the earliest block, the entire
		// target day is before the node's available range.
		if firstOfNextDay <= earliest {
			return 0, fmt.Errorf("date %s is before the node's earliest available block", input)
		}
		return firstOfNextDay - 1, nil
	}

	// Exact datetime: find first block at or after that time.
	return findFirstBlockAtOrAfter(ctx, client, targetTime, earliest, latest)
}
