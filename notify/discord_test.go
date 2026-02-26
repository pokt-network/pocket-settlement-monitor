package notify

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/store"
)

func TestFormatPOKT(t *testing.T) {
	tests := []struct {
		name     string
		upokt    int64
		expected string
	}{
		{name: "zero", upokt: 0, expected: "0.000000 POKT"},
		{name: "one POKT", upokt: 1_000_000, expected: "1.000000 POKT"},
		{name: "with commas and decimals", upokt: 1_234_560_000, expected: "1,234.560000 POKT"},
		{name: "sub-one POKT full precision", upokt: 999_999, expected: "0.999999 POKT"},
		{name: "negative value", upokt: -5_000_000, expected: "-5.000000 POKT"},
		{name: "large value", upokt: 1_000_000_000_000, expected: "1,000,000.000000 POKT"},
		{name: "small fraction", upokt: 10_000, expected: "0.010000 POKT"},
		{name: "exactly half", upokt: 1_500_000, expected: "1.500000 POKT"},
		{name: "very small beta amount", upokt: 576, expected: "0.000576 POKT"},
		{name: "single beta settlement", upokt: 18, expected: "0.000018 POKT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPOKT(tt.upokt)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateAddress(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected string
	}{
		{
			name:     "short address unchanged",
			addr:     "pokt1abc",
			expected: "pokt1abc",
		},
		{
			name:     "exactly 14 chars unchanged",
			addr:     "pokt1abcdefghi",
			expected: "pokt1abcdefghi",
		},
		{
			name:     "standard bech32 truncated",
			addr:     "pokt1abcdefghijklmnopqrstuvwxyz0123456789abcd",
			expected: "pokt1abcde...abcd",
		},
		{
			name:     "empty string",
			addr:     "",
			expected: "",
		},
		{
			name:     "15 chars truncated",
			addr:     "pokt1abcdefghij",
			expected: "pokt1abcde...ghij",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateAddress(tt.addr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestColorConstants(t *testing.T) {
	assert.Equal(t, 16711680, ColorSlash, "ColorSlash should be red (0xFF0000)")
	assert.Equal(t, 16747520, ColorExpiration, "ColorExpiration should be dark orange (0xFF8C00)")
	assert.Equal(t, 3066993, ColorSettlement, "ColorSettlement should be green (0x2ECC71)")
	assert.Equal(t, 15844367, ColorOverservice, "ColorOverservice should be yellow (0xF1C40F)")
	assert.Equal(t, 3447003, ColorSummary, "ColorSummary should be blue (0x3498DB)")
	assert.Equal(t, 9807270, ColorDiscard, "ColorDiscard should be gray (0x95A5A6)")
}

func TestBuildBlockEmbeds(t *testing.T) {
	ts := time.Date(2026, 2, 18, 14, 30, 0, 0, time.UTC)

	allEnabledCfg := config.NotificationsConfig{
		NotifySettlements: true,
		NotifyExpirations: true,
		NotifySlashes:     true,
		NotifyDiscards:    true,
		NotifyOverservice: true,
	}

	t.Run("two settled events produce one green embed", func(t *testing.T) {
		f := newDiscordFormatter(allEnabledCfg)

		settlements := []store.Settlement{
			{EventType: "settled", ClaimedUpokt: 1_000_000, NumRelays: 100, SupplierOperatorAddress: "pokt1supplier1addr0000000000000000000000001", ServiceID: "anvil"},
			{EventType: "settled", ClaimedUpokt: 2_000_000, NumRelays: 200, SupplierOperatorAddress: "pokt1supplier2addr0000000000000000000000002", ServiceID: "ethereum"},
		}

		payloads := f.buildBlockEmbeds(12345, ts, settlements, nil, nil)
		require.Len(t, payloads, 1)
		assert.False(t, payloads[0].isSlash)
		require.Len(t, payloads[0].Embeds, 1)
		assert.Equal(t, ColorSettlement, payloads[0].Embeds[0].Color)
		assert.Contains(t, payloads[0].Embeds[0].Title, "Block 12345")
		assert.Contains(t, payloads[0].Embeds[0].Title, "2 Settlements")
	})

	t.Run("slash plus settled produce two payloads with correct colors", func(t *testing.T) {
		f := newDiscordFormatter(allEnabledCfg)

		settlements := []store.Settlement{
			{EventType: "settled", ClaimedUpokt: 1_000_000, NumRelays: 100, SupplierOperatorAddress: "pokt1supplier1addr0000000000000000000000001", ServiceID: "anvil"},
			{EventType: "settled", ClaimedUpokt: 2_000_000, NumRelays: 200, SupplierOperatorAddress: "pokt1supplier2addr0000000000000000000000002", ServiceID: "ethereum"},
			{EventType: "slashed", ClaimedUpokt: 500_000, SlashPenaltyUpokt: 100_000, SupplierOperatorAddress: "pokt1supplier3addr0000000000000000000000003", ServiceID: "anvil"},
		}

		payloads := f.buildBlockEmbeds(12345, ts, settlements, nil, nil)
		require.Len(t, payloads, 2)

		// First payload should be green (settlements come first).
		assert.Equal(t, ColorSettlement, payloads[0].Embeds[0].Color)
		assert.False(t, payloads[0].isSlash)

		// Second payload should be red (slash).
		assert.Equal(t, ColorSlash, payloads[1].Embeds[0].Color)
		assert.True(t, payloads[1].isSlash)
	})

	t.Run("all types disabled returns empty", func(t *testing.T) {
		f := newDiscordFormatter(config.NotificationsConfig{
			NotifySettlements: false,
			NotifyExpirations: false,
			NotifySlashes:     false,
			NotifyDiscards:    false,
			NotifyOverservice: false,
		})

		settlements := []store.Settlement{
			{EventType: "settled", ClaimedUpokt: 1_000_000},
			{EventType: "expired", ClaimedUpokt: 500_000},
		}
		overservices := []store.OverserviceEvent{
			{ExpectedBurnUpokt: 100_000, EffectiveBurnUpokt: 80_000},
		}

		payloads := f.buildBlockEmbeds(12345, ts, settlements, overservices, nil)
		assert.Empty(t, payloads)
	})

	t.Run("zero events returns empty", func(t *testing.T) {
		f := newDiscordFormatter(allEnabledCfg)
		payloads := f.buildBlockEmbeds(12345, ts, nil, nil, nil)
		assert.Empty(t, payloads)
	})

	t.Run("mixed settled plus expired plus overservice produce three embeds", func(t *testing.T) {
		f := newDiscordFormatter(allEnabledCfg)

		settlements := []store.Settlement{
			{EventType: "settled", ClaimedUpokt: 1_000_000, NumRelays: 100, SupplierOperatorAddress: "pokt1supplier1addr0000000000000000000000001", ServiceID: "anvil"},
			{EventType: "expired", ClaimedUpokt: 500_000, NumRelays: 50, SupplierOperatorAddress: "pokt1supplier2addr0000000000000000000000002", ServiceID: "ethereum"},
		}
		overservices := []store.OverserviceEvent{
			{ExpectedBurnUpokt: 200_000, EffectiveBurnUpokt: 150_000, SupplierOperatorAddress: "pokt1supplier1addr0000000000000000000000001", ApplicationAddress: "pokt1app1addr00000000000000000000000000001"},
		}

		payloads := f.buildBlockEmbeds(12345, ts, settlements, overservices, nil)
		require.Len(t, payloads, 3)

		// Verify colors: green (settled), orange (expired), yellow (overservice).
		assert.Equal(t, ColorSettlement, payloads[0].Embeds[0].Color)
		assert.Equal(t, ColorExpiration, payloads[1].Embeds[0].Color)
		assert.Equal(t, ColorOverservice, payloads[2].Embeds[0].Color)
	})

	t.Run("embed has correct timestamp and footer", func(t *testing.T) {
		f := newDiscordFormatter(allEnabledCfg)

		settlements := []store.Settlement{
			{EventType: "settled", ClaimedUpokt: 1_000_000, NumRelays: 100, SupplierOperatorAddress: "pokt1supplier1addr", ServiceID: "anvil"},
		}

		payloads := f.buildBlockEmbeds(12345, ts, settlements, nil, nil)
		require.Len(t, payloads, 1)
		require.Len(t, payloads[0].Embeds, 1)

		e := payloads[0].Embeds[0]
		assert.Equal(t, ts.Format(time.RFC3339), e.Timestamp)
		require.NotNil(t, e.Footer)
		assert.Equal(t, footerText, e.Footer.Text)
	})
}

func TestBuildHourlySummaryEmbed(t *testing.T) {
	t.Run("zero value summary produces valid blue embed", func(t *testing.T) {
		f := newDiscordFormatter(config.NotificationsConfig{})
		hourStart := time.Date(2026, 2, 18, 14, 0, 0, 0, time.UTC)

		summary := store.HourlySummaryNetwork{
			HourStart: hourStart,
		}

		payload := f.buildHourlySummaryEmbed(summary)
		require.Len(t, payload.Embeds, 1)

		e := payload.Embeds[0]
		assert.Equal(t, ColorSummary, e.Color)
		assert.Contains(t, e.Title, "Hourly Summary")
		assert.Contains(t, e.Title, "2026-02-18 14:00 UTC")
		assert.NotEmpty(t, e.Fields)

		// Check the POKT Earned field shows zero.
		var foundEarned bool
		for _, f := range e.Fields {
			if f.Name == "POKT Earned" {
				assert.Equal(t, "0.000000 POKT", f.Value)
				foundEarned = true
			}
		}
		assert.True(t, foundEarned, "should have POKT Earned field")
	})

	t.Run("non-zero summary has formatted POKT values", func(t *testing.T) {
		f := newDiscordFormatter(config.NotificationsConfig{})
		hourStart := time.Date(2026, 2, 18, 15, 0, 0, 0, time.UTC)

		summary := store.HourlySummaryNetwork{
			HourStart:           hourStart,
			ClaimsSettled:       12,
			ClaimsExpired:       2,
			ClaimsSlashed:       0,
			ClaimsDiscarded:     1,
			ClaimedTotalUpokt:   12_345_670_000,
			EffectiveTotalUpokt: 12_000_000_000,
			NumRelays:           5_678_901,
			EstimatedRelays:     6_000_000,
			OverserviceCount:    3,
		}

		payload := f.buildHourlySummaryEmbed(summary)
		require.Len(t, payload.Embeds, 1)

		e := payload.Embeds[0]
		assert.Equal(t, ColorSummary, e.Color)

		// Check specific field values.
		fieldMap := make(map[string]string)
		for _, f := range e.Fields {
			fieldMap[f.Name] = f.Value
		}

		assert.Equal(t, "12", fieldMap["Settled"])
		assert.Equal(t, "2", fieldMap["Expired"])
		assert.Equal(t, "0", fieldMap["Slashed"])
		assert.Equal(t, "1", fieldMap["Discarded"])
		assert.Equal(t, "12,345.670000 POKT", fieldMap["POKT Earned"])
		assert.Equal(t, "345.670000 POKT", fieldMap["POKT Lost"])
		assert.Equal(t, "5,678,901", fieldMap["Total Relays"])
		assert.Equal(t, "6,000,000", fieldMap["Est. Relays"])
		assert.Equal(t, "3", fieldMap["Overserviced"])
	})
}

func TestBuildDailySummaryEmbed(t *testing.T) {
	t.Run("daily summary with previous day comparison", func(t *testing.T) {
		f := newDiscordFormatter(config.NotificationsConfig{})
		today := time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC)

		summary := store.DailySummaryNetwork{
			DayDate:             today,
			ClaimsSettled:       100,
			ClaimsExpired:       10,
			ClaimsSlashed:       1,
			ClaimedTotalUpokt:   100_000_000_000,
			EffectiveTotalUpokt: 95_000_000_000,
			NumRelays:           1_000_000,
			EstimatedRelays:     1_200_000,
			OverserviceCount:    5,
		}

		prevDay := store.DailySummaryNetwork{
			DayDate:             today.AddDate(0, 0, -1),
			ClaimsSettled:       80,
			ClaimsExpired:       15,
			ClaimsSlashed:       0,
			ClaimedTotalUpokt:   80_000_000_000,
			EffectiveTotalUpokt: 78_000_000_000,
			NumRelays:           800_000,
		}

		payload := f.buildDailySummaryEmbed(summary, prevDay, "pokt1abc...xyz: 50,000 POKT")
		require.Len(t, payload.Embeds, 1)

		e := payload.Embeds[0]
		assert.Equal(t, ColorSummary, e.Color)
		assert.Contains(t, e.Title, "Daily Summary")
		assert.Contains(t, e.Title, "2026-02-18")

		// Check that comparison field exists.
		fieldMap := make(map[string]string)
		for _, f := range e.Fields {
			fieldMap[f.Name] = f.Value
		}

		// Verify comparison includes percentage.
		compValue, exists := fieldMap["vs Previous Day"]
		assert.True(t, exists, "should have comparison field")
		assert.Contains(t, compValue, "POKT earned")
		assert.Contains(t, compValue, "+20 settled claims")

		// Verify supplier breakdown is included.
		breakdownValue, exists := fieldMap["Supplier Breakdown"]
		assert.True(t, exists, "should have supplier breakdown field")
		assert.Contains(t, breakdownValue, "pokt1abc...xyz")
	})

	t.Run("previous day is zero handles gracefully", func(t *testing.T) {
		f := newDiscordFormatter(config.NotificationsConfig{})
		today := time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC)

		summary := store.DailySummaryNetwork{
			DayDate:             today,
			ClaimsSettled:       10,
			ClaimedTotalUpokt:   10_000_000_000,
			EffectiveTotalUpokt: 9_500_000_000,
		}

		prevDay := store.DailySummaryNetwork{} // zero value

		payload := f.buildDailySummaryEmbed(summary, prevDay, "")
		require.Len(t, payload.Embeds, 1)

		e := payload.Embeds[0]
		assert.Equal(t, ColorSummary, e.Color)

		// Check that comparison does not panic and handles zero division.
		fieldMap := make(map[string]string)
		for _, f := range e.Fields {
			fieldMap[f.Name] = f.Value
		}

		compValue, exists := fieldMap["vs Previous Day"]
		assert.True(t, exists, "should have comparison field even with zero previous")
		// Should not contain NaN or Inf.
		assert.NotContains(t, compValue, "NaN")
		assert.NotContains(t, compValue, "Inf")
		// Should show "new" since previous was 0.
		assert.Contains(t, compValue, "new")
	})
}

func TestFormatComparison(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		previous int64
		label    string
		expected string
	}{
		{
			name:     "no change",
			current:  100,
			previous: 100,
			label:    "POKT earned",
			expected: "",
		},
		{
			name:     "increase from zero",
			current:  1_000_000,
			previous: 0,
			label:    "POKT earned",
			expected: "1.000000 POKT POKT earned (new)",
		},
		{
			name:     "25 percent increase",
			current:  125,
			previous: 100,
			label:    "POKT earned",
			expected: "+25.0% POKT earned",
		},
		{
			name:     "50 percent decrease",
			current:  50,
			previous: 100,
			label:    "POKT lost",
			expected: "-50.0% POKT lost",
		},
		{
			name:     "both zero",
			current:  0,
			previous: 0,
			label:    "POKT earned",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatComparison(tt.current, tt.previous, tt.label)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEstimateEmbedChars(t *testing.T) {
	fields := []embedField{
		{Name: "Field1", Value: "Value1"},
		{Name: "Field2", Value: "Value2"},
	}
	total := estimateEmbedChars("Title", fields)
	// "Title" = 5, "Field1" = 6, "Value1" = 6, "Field2" = 6, "Value2" = 6 = 29
	assert.Equal(t, 29, total)
}

func TestFormatWithCommas(t *testing.T) {
	tests := []struct {
		name     string
		n        int64
		expected string
	}{
		{name: "zero", n: 0, expected: "0"},
		{name: "no commas needed", n: 999, expected: "999"},
		{name: "one comma", n: 1000, expected: "1,000"},
		{name: "two commas", n: 1_000_000, expected: "1,000,000"},
		{name: "irregular digits", n: 12345, expected: "12,345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatWithCommas(tt.n)
			assert.Equal(t, tt.expected, result)
		})
	}
}
