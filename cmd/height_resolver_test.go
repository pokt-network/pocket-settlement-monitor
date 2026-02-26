package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlexibleTime_RFC3339(t *testing.T) {
	result, err := parseFlexibleTime("2024-01-15T10:30:00Z")
	require.NoError(t, err)
	expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	assert.Equal(t, expected, result)
}

func TestParseFlexibleTime_RFC3339WithOffset(t *testing.T) {
	result, err := parseFlexibleTime("2024-01-15T10:30:00-05:00")
	require.NoError(t, err)
	// -05:00 means 10:30 local = 15:30 UTC.
	expected := time.Date(2024, 1, 15, 15, 30, 0, 0, time.UTC)
	assert.Equal(t, expected, result)
}

func TestParseFlexibleTime_DatetimeNoTZ(t *testing.T) {
	result, err := parseFlexibleTime("2024-01-15T10:30:00")
	require.NoError(t, err)
	expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	assert.Equal(t, expected, result)
}

func TestParseFlexibleTime_DateOnly(t *testing.T) {
	result, err := parseFlexibleTime("2024-01-15")
	require.NoError(t, err)
	expected := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, result)
}

func TestParseFlexibleTime_Invalid(t *testing.T) {
	_, err := parseFlexibleTime("not-a-date")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse")
	assert.Contains(t, err.Error(), "accepted formats")
}
