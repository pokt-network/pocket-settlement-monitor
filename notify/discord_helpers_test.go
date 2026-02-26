package notify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/store"
)

// ---------------------------------------------------------------------------
// sortedKeyList
// ---------------------------------------------------------------------------

func TestSortedKeyList_Empty(t *testing.T) {
	result := sortedKeyList(map[string]struct{}{})
	assert.Equal(t, "", result)
}

func TestSortedKeyList_SingleKey(t *testing.T) {
	m := map[string]struct{}{"anvil": {}}
	result := sortedKeyList(m)
	assert.Equal(t, "anvil", result)
}

func TestSortedKeyList_MultipleKeys(t *testing.T) {
	m := map[string]struct{}{
		"anvil":   {},
		"relay":   {},
		"gateway": {},
	}
	result := sortedKeyList(m)

	// Go map iteration order is non-deterministic, so we cannot assert
	// exact string equality. Instead verify every key is present and the
	// number of separators is correct.
	for key := range m {
		assert.True(t, strings.Contains(result, key), "result should contain key %q, got %q", key, result)
	}
	assert.Equal(t, 2, strings.Count(result, ", "), "expected 2 comma-separators for 3 keys")
}

// ---------------------------------------------------------------------------
// addressCount
// ---------------------------------------------------------------------------

func TestAddressCount_Empty(t *testing.T) {
	result := addressCount(map[string]struct{}{})
	assert.Equal(t, "N/A", result)
}

func TestAddressCount_Single(t *testing.T) {
	m := map[string]struct{}{"pokt1abc": {}}
	result := addressCount(m)
	assert.Equal(t, "1", result)
}

func TestAddressCount_Multiple(t *testing.T) {
	m := map[string]struct{}{
		"pokt1abc": {},
		"pokt1def": {},
		"pokt1ghi": {},
	}
	result := addressCount(m)
	assert.Equal(t, "3", result)
}

func TestAddressCount_Large(t *testing.T) {
	m := make(map[string]struct{})
	for i := 0; i < 2200; i++ {
		m[fmt.Sprintf("pokt1%04d", i)] = struct{}{}
	}
	result := addressCount(m)
	assert.Equal(t, "2,200", result)
}

// ---------------------------------------------------------------------------
// compactSettlementSummary
// ---------------------------------------------------------------------------

func TestCompactSettlementSummary_NonSlash(t *testing.T) {
	f := newDiscordFormatter(config.NotificationsConfig{})
	settlements := make([]store.Settlement, 5)

	result := f.compactSettlementSummary(settlements, 1_000_000, 100, 0, false)

	// Expected: "5 events: 1.000000 POKT, 100 relays"
	assert.Equal(t, "5 events: 1.000000 POKT, 100 relays", result)
}

func TestCompactSettlementSummary_Slash(t *testing.T) {
	f := newDiscordFormatter(config.NotificationsConfig{})
	settlements := make([]store.Settlement, 3)

	result := f.compactSettlementSummary(settlements, 2_000_000, 500, 500_000, true)

	// Expected: "3 events: penalty 0.500000 POKT, claimed 2.000000 POKT, 500 relays"
	assert.Equal(t, "3 events: penalty 0.500000 POKT, claimed 2.000000 POKT, 500 relays", result)
}
