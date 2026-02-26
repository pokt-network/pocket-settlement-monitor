package metrics

import "github.com/prometheus/client_golang/prometheus"

// LabelConfig controls which optional label dimensions are included on metrics.
// All toggles default to false (minimal cardinality out of the box).
type LabelConfig struct {
	IncludeSupplier    bool `yaml:"include_supplier"`
	IncludeService     bool `yaml:"include_service"`
	IncludeApplication bool `yaml:"include_application"`
}

// SupplierLabel returns the supplier address if the supplier dimension is enabled,
// otherwise returns an empty string.
func (lc *LabelConfig) SupplierLabel(addr string) string {
	if lc.IncludeSupplier {
		return addr
	}
	return ""
}

// ServiceLabel returns the service ID if the service dimension is enabled,
// otherwise returns an empty string.
func (lc *LabelConfig) ServiceLabel(svc string) string {
	if lc.IncludeService {
		return svc
	}
	return ""
}

// ApplicationLabel returns the application address if the application dimension is enabled,
// otherwise returns an empty string.
func (lc *LabelConfig) ApplicationLabel(app string) string {
	if lc.IncludeApplication {
		return app
	}
	return ""
}

// Labels builds a full Prometheus label map using the config toggles.
// event_type is always included; supplier, service, and application are
// included only if their respective toggles are enabled.
func (lc *LabelConfig) Labels(eventType, supplier, service, application string) prometheus.Labels {
	return prometheus.Labels{
		"event_type":  eventType,
		"supplier":    lc.SupplierLabel(supplier),
		"service":     lc.ServiceLabel(service),
		"application": lc.ApplicationLabel(application),
	}
}
