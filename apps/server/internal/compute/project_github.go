package compute

import (
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// ProjectGitHubAssociation records a reviewed integration, never credentials.
// Its revision is independent of project/file/environment revisions.
type ProjectGitHubAssociation struct {
	Repository protocol.GitHubRepository `json:"repository"`
	Account    protocol.GitHubAccount    `json:"account"`
	MachineID  string                    `json:"machineId"`
	Profile    string                    `json:"profile"`
	UpdatedAt  time.Time                 `json:"updatedAt"`
}

type ProjectGitHubState struct {
	Revision    int64                     `json:"revision"`
	Association *ProjectGitHubAssociation `json:"association,omitempty"`
}

type ProjectGitHubRequest struct {
	Binding      ProjectBinding `json:"binding"`
	Revision     int64          `json:"revision"`
	Repository   string         `json:"repository,omitempty"`
	RepositoryID string         `json:"repositoryId,omitempty"`
	AccountID    string         `json:"accountId,omitempty"`
}

type ProjectGitHubResult struct {
	Binding     ProjectBinding                    `json:"binding"`
	Revision    int64                             `json:"revision"`
	Observation protocol.ProjectGitHubObservation `json:"observation"`
}
