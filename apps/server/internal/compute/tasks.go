package compute

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type TaskSpec = protocol.TaskSpec
type TaskLimits = protocol.TaskLimits

type ProjectTask struct {
	ID               string    `json:"id"`
	HostID           string    `json:"hostId"`
	ProjectID        string    `json:"projectId"`
	RequestID        string    `json:"requestId"`
	Spec             TaskSpec  `json:"spec"`
	CurrentAttemptID string    `json:"currentAttemptId"`
	CreatedAt        time.Time `json:"createdAt"`
}

type TaskAttempt struct {
	CreatedRoot  *protocol.ProjectFileRoot `json:"createdRoot,omitempty"`
	ID           string                    `json:"id"`
	TaskID       string                    `json:"taskId"`
	Number       int                       `json:"number"`
	Binding      ProjectBinding            `json:"binding"`
	Root         string                    `json:"root"`
	RootIdentity string                    `json:"rootIdentity"`
	RequestID    string                    `json:"requestId"`
	Status       string                    `json:"status"`
	Reason       string                    `json:"reason,omitempty"`
	SyncError    string                    `json:"syncError,omitempty"`
	ExitCode     *int                      `json:"exitCode,omitempty"`
	CreatedAt    time.Time                 `json:"createdAt"`
	StartedAt    *time.Time                `json:"startedAt,omitempty"`
	FinishedAt   *time.Time                `json:"finishedAt,omitempty"`
	LogBytes     int64                     `json:"logBytes"`
	CachedBytes  int64                     `json:"cachedBytes"`
	LogLimited   bool                      `json:"logLimited"`
	GuestFinal   bool                      `json:"guestFinal"`
	Cancel       bool                      `json:"cancelRequested"`
}

type ProjectTaskResult struct {
	Task     ProjectTask   `json:"task"`
	Attempt  TaskAttempt   `json:"attempt"`
	Attempts []TaskAttempt `json:"attempts"`
}

type SubmitProjectTaskRequest struct {
	RequestID string         `json:"requestId"`
	Binding   ProjectBinding `json:"binding"`
	Spec      TaskSpec       `json:"spec"`
}

type RetryProjectTaskRequest struct {
	RequestID string         `json:"requestId"`
	Binding   ProjectBinding `json:"binding"`
}

type CancelProjectTaskRequest struct {
	Binding ProjectBinding `json:"binding"`
}

type ProjectTaskLog struct {
	TaskID    string `json:"taskId"`
	AttemptID string `json:"attemptId"`
	Status    string `json:"status"`
	protocol.TaskLog
}

func TaskFinal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}

// NormalizeTaskSpec applies explicit defaults before fingerprints are computed.
func NormalizeTaskSpec(spec TaskSpec) TaskSpec {
	if spec.Limits.TimeoutSeconds == 0 {
		spec.Limits.TimeoutSeconds = 900
	}
	if spec.Limits.CPUs == 0 {
		spec.Limits.CPUs = 1
	}
	if spec.Limits.MemoryMiB == 0 {
		spec.Limits.MemoryMiB = 512
	}
	if spec.Limits.MaxProcesses == 0 {
		spec.Limits.MaxProcesses = 64
	}
	if spec.Limits.MaxLogBytes == 0 {
		spec.Limits.MaxLogBytes = 1 << 20
	}
	return spec
}

func ValidateTaskSpec(spec TaskSpec) error {
	if spec.Initialization != nil {
		return Problem("invalid_request", "Project initialization intent is reserved for the initialization API.")
	}
	return validateTaskSpec(spec)
}

func ValidateInitializationTaskSpec(spec TaskSpec) error {
	if spec.Initialization == nil || len(spec.Argv) != 0 || spec.Directory != "" {
		return Problem("invalid_request", "Initialization requires typed intent without an ordinary command or starting directory.")
	}
	if err := spec.Initialization.Validate(); err != nil {
		return Problem("invalid_request", "%v", err)
	}
	validation := spec
	validation.Initialization = nil
	validation.Argv = []string{"/bin/true"}
	return validateTaskSpec(validation)
}

func validateTaskSpec(spec TaskSpec) error {
	if err := ValidateProjectRoot(spec.Directory); err != nil {
		return err
	}
	if len(spec.Argv) == 0 || len(spec.Argv) > 128 {
		return Problem("invalid_request", "Supply 1–128 task arguments.")
	}
	bytes := 0
	for _, arg := range spec.Argv {
		bytes += len(arg)
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return Problem("invalid_request", "Task arguments require valid UTF-8 without NUL.")
		}
	}
	if spec.Argv[0] == "" || bytes > 32<<10 {
		return Problem("invalid_request", "Task arguments exceed their limit.")
	}
	l := spec.Limits
	if l.TimeoutSeconds < 1 || l.TimeoutSeconds > 86400 || l.CPUs < 1 || l.CPUs > 128 || l.MemoryMiB < 64 || l.MemoryMiB > 1<<20 || l.MaxProcesses < 2 || l.MaxProcesses > 256 || l.MaxLogBytes < 1024 || l.MaxLogBytes > 8<<20 {
		return Problem("invalid_request", "Task limits require timeout 1–86400 seconds, CPUs 1–128, memory >=64 MiB, processes 2–256, and logs 1 KiB–8 MiB.")
	}
	return nil
}
