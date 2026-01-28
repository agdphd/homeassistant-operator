package utils

// E2E Test Resource Requests and Limits
//
// These constants define standard resource requests and limits used across all E2E tests.
// They are centralized here to ensure consistency and make it easy to adjust
// resource allocations globally if needed (e.g., for CI environments).
//
// Resource requests help Kubernetes scheduler place pods appropriately and
// ensure Home Assistant has minimum resources to start reliably.

const (
	// DefaultHACPURequest is the default CPU request for Home Assistant pods in E2E tests.
	// 100m (0.1 CPU cores) is sufficient for test scenarios with minimal load.
	DefaultHACPURequest = "100m"

	// DefaultHAMemoryRequest is the default memory request for Home Assistant pods in E2E tests.
	// 256Mi is the minimum recommended for Home Assistant to start reliably.
	DefaultHAMemoryRequest = "256Mi"

	// DefaultHAMemoryLimit is the default memory limit for Home Assistant pods in E2E tests.
	// 512Mi provides headroom for first boot and database initialization.
	DefaultHAMemoryLimit = "512Mi"

	// EnhancedHACPURequest is a higher CPU request for tests requiring better performance.
	// Used when faster startup or more responsive operations are needed.
	EnhancedHACPURequest = "500m"

	// EnhancedHAMemoryRequest is a higher memory request for tests with larger workloads.
	// Used for scenarios with more integrations or data processing.
	EnhancedHAMemoryRequest = "512Mi"

	// EnhancedHAMemoryLimit is a higher memory limit for resource-intensive test scenarios.
	EnhancedHAMemoryLimit = "1Gi"
)

// GetDefaultHAResourceRequests returns the standard resource requests YAML block
// for Home Assistant pods in E2E tests. This ensures consistent resource
// allocation across all test scenarios.
//
// Example usage in test YAML:
//
//	resources:
//	  %s
func GetDefaultHAResourceRequests() string {
	return `requests:
      cpu: "` + DefaultHACPURequest + `"
      memory: "` + DefaultHAMemoryRequest + `"
    limits:
      memory: "` + DefaultHAMemoryLimit + `"`
}

// GetEnhancedHAResourceRequests returns higher resource requests for tests
// requiring better performance or handling larger workloads.
//
// Example usage in test YAML:
//
//	resources:
//	  %s
func GetEnhancedHAResourceRequests() string {
	return `requests:
      cpu: "` + EnhancedHACPURequest + `"
      memory: "` + EnhancedHAMemoryRequest + `"
    limits:
      memory: "` + EnhancedHAMemoryLimit + `"`
}
