package collector

// Collector defines the interface that every plugin module must implement.
type Collector interface {
	// Name returns the name of the collector (e.g., "BrowserModule", "ProcessMonitor")
	Name() string

	// Start begins the collection process, typically running in a goroutine
	Start() error

	// Stop gracefully shuts down the collector
	Stop() error

	// Collect gathers the current telemetry data and returns it as a generic map or struct
	Collect() (map[string]interface{}, error)
}
