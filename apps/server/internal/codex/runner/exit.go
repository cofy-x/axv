package runner

// Exit proves that one exact runner has finished cleaning up its owned runtime.
// It is separate from readiness state and is never inferred from an empty pane.
type Exit struct {
	RunID    string `json:"runId"`
	ExitCode int    `json:"exitCode"`
}
