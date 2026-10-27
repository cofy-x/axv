package desktop

// Snapshot combines the saved preference with the actual desktop listener status.
type Snapshot struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
	Status
}
