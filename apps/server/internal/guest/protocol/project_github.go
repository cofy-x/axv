package protocol

// ProjectGitHubRequest exposes only fixed project metadata and GitHub reads.
// Repository names are reviewed candidates; immutable IDs fence later reads.
type ProjectGitHubRequest struct {
	ProjectFileScope
	Repository   string `json:"repository,omitempty"`
	RepositoryID string `json:"repositoryId,omitempty"`
	AccountID    string `json:"accountId,omitempty"`
}

type GitHubRemote struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
}

type GitHubGitState struct {
	Repository     bool           `json:"repository"`
	HeadSHA        string         `json:"headSha,omitempty"`
	Branch         string         `json:"branch,omitempty"`
	Detached       bool           `json:"detached"`
	Unborn         bool           `json:"unborn"`
	Remotes        []GitHubRemote `json:"remotes"`
	RemotesPartial bool           `json:"remotesPartial"`
}

type GitHubRepository struct {
	ID            string `json:"id"`
	NameWithOwner string `json:"nameWithOwner"`
	URL           string `json:"url"`
}

type GitHubAccount struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}

type GitHubCheck struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url,omitempty"`
}

type GitHubCheckSet struct {
	Repository GitHubRepository `json:"repository"`
	SHA        string           `json:"sha"`
	State      string           `json:"state"`
	Complete   bool             `json:"complete"`
	Checks     []GitHubCheck    `json:"checks"`
}

type GitHubPullRequest struct {
	ID             string           `json:"id"`
	Number         int              `json:"number"`
	Title          string           `json:"title"`
	URL            string           `json:"url"`
	State          string           `json:"state"`
	BaseRepository GitHubRepository `json:"baseRepository"`
	HeadRepository GitHubRepository `json:"headRepository"`
	HeadBranch     string           `json:"headBranch"`
	HeadSHA        string           `json:"headSha"`
	HeadChecks     *GitHubCheckSet  `json:"headChecks,omitempty"`
	MergeChecks    *GitHubCheckSet  `json:"mergeChecks,omitempty"`
}

type ProjectGitHubObservation struct {
	Git                  GitHubGitState      `json:"git"`
	Account              GitHubAccount       `json:"account"`
	Repository           GitHubRepository    `json:"repository"`
	LocalChecks          *GitHubCheckSet     `json:"localChecks,omitempty"`
	PullRequests         []GitHubPullRequest `json:"pullRequests"`
	PullRequestsComplete bool                `json:"pullRequestsComplete"`
	ObservedAt           string              `json:"observedAt"`
}
