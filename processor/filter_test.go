package processor

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSupplierFilter_WithAddresses(t *testing.T) {
	logger := zerolog.Nop()
	filter := NewSupplierFilter([]string{"pokt1abc", "pokt1xyz"}, logger)

	require.NotNil(t, filter)
	assert.False(t, filter.monitorAll)
}

func TestNewSupplierFilter_NilAddresses(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()

	filter := NewSupplierFilter(nil, logger)

	require.NotNil(t, filter)
	assert.True(t, filter.monitorAll)
	// Verify WARN log was emitted
	assert.Contains(t, buf.String(), "no supplier addresses configured, monitoring all events")
}

func TestNewSupplierFilter_EmptyAddresses(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()

	filter := NewSupplierFilter([]string{}, logger)

	require.NotNil(t, filter)
	assert.True(t, filter.monitorAll)
	assert.Contains(t, buf.String(), "no supplier addresses configured, monitoring all events")
}

func TestSupplierFilter_Match_Configured(t *testing.T) {
	logger := zerolog.Nop()
	filter := NewSupplierFilter([]string{"pokt1abc", "pokt1xyz"}, logger)

	assert.True(t, filter.Match("pokt1abc"))
	assert.True(t, filter.Match("pokt1xyz"))
	assert.False(t, filter.Match("pokt1other"))
	assert.False(t, filter.Match(""))
}

func TestSupplierFilter_Match_MonitorAll(t *testing.T) {
	logger := zerolog.Nop()
	filter := NewSupplierFilter(nil, logger)

	// Monitor-all mode: everything passes
	assert.True(t, filter.Match("pokt1abc"))
	assert.True(t, filter.Match("pokt1xyz"))
	assert.True(t, filter.Match("pokt1anything"))
	assert.True(t, filter.Match(""))
}

func TestSupplierFilter_Match_EmptyAddress(t *testing.T) {
	logger := zerolog.Nop()
	filter := NewSupplierFilter([]string{"pokt1abc"}, logger)

	// Empty address should NOT match when filter is configured
	assert.False(t, filter.Match(""))
}
