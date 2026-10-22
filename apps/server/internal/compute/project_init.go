package compute

import (
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// InitializeProjectRequest creates one new directory inside an explicitly opened
// parent project. Its request ID is the durable task submission identity.
type InitializeProjectRequest struct {
	RequestID  string         `json:"requestId"`
	Binding    ProjectBinding `json:"binding"`
	Directory  string         `json:"directory"`
	Repository string         `json:"repository,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// ValidateRepositoryURL shares the private guest initializer's URL policy.
func ValidateRepositoryURL(repository string) error {
	if err := protocol.ValidateRepositoryURL(repository); err != nil {
		return Problem("invalid_request", "%v", err)
	}
	return nil
}

func ProjectInitializeSpec(r InitializeProjectRequest) (TaskSpec, error) {
	if err := ValidateRequestID(r.RequestID); err != nil {
		return TaskSpec{}, err
	}
	intent := &protocol.ProjectInitialization{Directory: r.Directory, Repository: r.Repository, Name: r.Name}
	if err := intent.Validate(); err != nil {
		return TaskSpec{}, Problem("invalid_request", "%v", err)
	}
	spec := NormalizeTaskSpec(TaskSpec{Argv: []string{}, Initialization: intent})
	return spec, ValidateInitializationTaskSpec(spec)
}
