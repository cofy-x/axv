package protocol

import "time"

// TaskLimits are enforced inside the guest, independently for each attempt.
type TaskLimits struct {
	TimeoutSeconds int   `json:"timeoutSeconds"`
	CPUs           int   `json:"cpus"`
	MemoryMiB      int64 `json:"memoryMiB"`
	MaxProcesses   int   `json:"maxProcesses"`
	MaxLogBytes    int64 `json:"maxLogBytes"`
}

type TaskSpec struct {
	Argv      []string   `json:"argv"`
	Directory string     `json:"directory"`
	Limits    TaskLimits `json:"limits"`
}

type TaskSubmit struct {
	ProjectFileScope
	TaskID    string   `json:"taskId"`
	AttemptID string   `json:"attemptId"`
	Spec      TaskSpec `json:"spec"`
}

type TaskIdentity struct {
	ProjectFileScope
	TaskID    string `json:"taskId"`
	AttemptID string `json:"attemptId"`
}

type TaskLogRequest struct {
	TaskIdentity
	Offset int64 `json:"offset"`
	Limit  int   `json:"limit"`
}

// TaskExecution is the durable guest receipt/result for exactly one attempt.
// Unknown/interrupted attempts are never implicitly launched again.
type TaskExecution struct {
	TaskIdentity
	Spec       TaskSpec   `json:"spec"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	LogBytes   int64      `json:"logBytes"`
	LogLimited bool       `json:"logLimited"`
}

// Data is byte-exact combined stdout/stderr, encoded as base64 in JSON. Offsets
// remain stable even when output is not valid UTF-8 or the caller reconnects.
type TaskLog struct {
	Offset     int64  `json:"offset"`
	Data       []byte `json:"data"`
	NextOffset int64  `json:"nextOffset"`
	LogBytes   int64  `json:"logBytes"`
	LogLimited bool   `json:"logLimited"`
}
