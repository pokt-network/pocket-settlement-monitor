package processor

import (
	"github.com/rs/zerolog"
)

// SupplierFilter determines which supplier events to process.
// When configured with specific addresses, only events from those suppliers pass.
// When no addresses are configured (monitor-all mode), all events pass.
type SupplierFilter struct {
	addresses  map[string]bool
	monitorAll bool
}

// NewSupplierFilter creates a SupplierFilter from a list of supplier operator addresses.
// If the list is nil or empty, monitor-all mode is enabled and a WARN is logged.
func NewSupplierFilter(addresses []string, logger zerolog.Logger) *SupplierFilter {
	if len(addresses) == 0 {
		logger.Warn().Msg("no supplier addresses configured, monitoring all events")
		return &SupplierFilter{
			monitorAll: true,
		}
	}

	addrMap := make(map[string]bool, len(addresses))
	for _, addr := range addresses {
		addrMap[addr] = true
	}

	return &SupplierFilter{
		addresses:  addrMap,
		monitorAll: false,
	}
}

// Match returns true if the given supplier operator address passes the filter.
// In monitor-all mode (no addresses configured), always returns true.
func (f *SupplierFilter) Match(supplierOperatorAddress string) bool {
	if f.monitorAll {
		return true
	}
	return f.addresses[supplierOperatorAddress]
}
