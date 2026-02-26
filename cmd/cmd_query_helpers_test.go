package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQueryFilters_HeightInputs(t *testing.T) {
	qFrom = "100"
	qTo = "200"
	defer func() { qFrom = ""; qTo = "" }()

	_, _, fromHeight, toHeight, err := parseQueryFilters()
	require.NoError(t, err)
	assert.Equal(t, int64(100), fromHeight)
	assert.Equal(t, int64(200), toHeight)
}

func TestParseQueryFilters_TimeInputs(t *testing.T) {
	qFrom = "2024-01-15"
	qTo = "2024-01-20"
	defer func() { qFrom = ""; qTo = "" }()

	fromTime, toTime, fromHeight, toHeight, err := parseQueryFilters()
	require.NoError(t, err)
	assert.Equal(t, int64(0), fromHeight)
	assert.Equal(t, int64(0), toHeight)
	assert.Equal(t, 2024, fromTime.Year())
	assert.Equal(t, 1, int(fromTime.Month()))
	assert.Equal(t, 15, fromTime.Day())
	assert.Equal(t, 2024, toTime.Year())
	assert.Equal(t, 1, int(toTime.Month()))
	assert.Equal(t, 20, toTime.Day())
}

func TestParseQueryFilters_MixedInputs(t *testing.T) {
	qFrom = "100"
	qTo = "2024-01-20"
	defer func() { qFrom = ""; qTo = "" }()

	_, toTime, fromHeight, toHeight, err := parseQueryFilters()
	require.NoError(t, err)
	assert.Equal(t, int64(100), fromHeight)
	assert.Equal(t, int64(0), toHeight)
	assert.Equal(t, 2024, toTime.Year())
}

func TestParseQueryFilters_Empty(t *testing.T) {
	qFrom = ""
	qTo = ""

	fromTime, toTime, fromHeight, toHeight, err := parseQueryFilters()
	require.NoError(t, err)
	assert.True(t, fromTime.IsZero())
	assert.True(t, toTime.IsZero())
	assert.Equal(t, int64(0), fromHeight)
	assert.Equal(t, int64(0), toHeight)
}

func TestParseQueryFilters_InvalidFrom(t *testing.T) {
	qFrom = "garbage"
	qTo = ""
	defer func() { qFrom = "" }()

	_, _, _, _, err := parseQueryFilters()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --from value")
}

func TestParseQueryFilters_InvalidTo(t *testing.T) {
	qFrom = ""
	qTo = "garbage"
	defer func() { qTo = "" }()

	_, _, _, _, err := parseQueryFilters()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --to value")
}

func TestValidateOutputFormat_Valid(t *testing.T) {
	for _, format := range []string{"table", "json", "csv"} {
		qOutput = format
		err := validateOutputFormat()
		assert.NoError(t, err, "format %q should be valid", format)
	}
	qOutput = "table" // restore default
}

func TestValidateOutputFormat_Invalid(t *testing.T) {
	qOutput = "xml"
	defer func() { qOutput = "table" }()

	err := validateOutputFormat()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
}

func TestTruncAddr_Long(t *testing.T) {
	// 20 chars — exceeds 15 threshold.
	addr := "pokt1abcdefghijklmno"
	result := truncAddr(addr)
	assert.Equal(t, "pokt1abcdefg...", result)
}

func TestTruncAddr_Short(t *testing.T) {
	addr := "pokt1abc"
	result := truncAddr(addr)
	assert.Equal(t, "pokt1abc", result)
}

func TestTruncAddr_ExactlyFifteen(t *testing.T) {
	// Exactly 15 chars — should NOT be truncated (> 15 is the condition).
	addr := "pokt1abcdefghij"
	result := truncAddr(addr)
	assert.Equal(t, "pokt1abcdefghij", result)
}

func TestTruncAddr_Sixteen(t *testing.T) {
	// 16 chars — should be truncated.
	addr := "pokt1abcdefghijk"
	result := truncAddr(addr)
	assert.Equal(t, "pokt1abcdefg...", result)
}
