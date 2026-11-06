package control

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

var githubRepositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)

func githubState(p compute.Project) compute.ProjectGitHubState {
	return compute.ProjectGitHubState{Revision: p.GitHubRevision, Association: p.GitHub}
}

func (s *Service) ProjectGitHubState(ctx context.Context, id, hostID string) (compute.ProjectGitHubState, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return compute.ProjectGitHubState{}, err
	}
	p, err := s.store.Project(ctx, id)
	return githubState(p), err
}

func validateProjectGitHub(id, action string, r compute.ProjectGitHubRequest) error {
	b := r.Binding
	for _, v := range []string{id, b.HostID, b.ProjectID, b.VolumeID, b.MachineID, b.BootID, b.InstanceID, r.Repository, r.RepositoryID, r.AccountID} {
		if !utf8.ValidString(v) || len(v) > 256 {
			return compute.Problem("invalid_request", "GitHub request identifiers must be bounded valid UTF-8.")
		}
	}
	if b.ProjectID != id || b.HostID == "" || b.VolumeID == "" || b.MachineID == "" || b.BootID == "" || b.InstanceID == "" || b.ProjectRevision < 1 || b.VolumeRevision < 1 {
		return compute.Problem("binding_changed", "The original complete project environment is required for GitHub reads.")
	}
	if r.Revision < 0 || r.Revision >= 9007199254740991 {
		return compute.Problem("invalid_request", "Supply the reviewed GitHub association revision.")
	}
	switch action {
	case "git", "clear", "refresh":
		if r.Repository != "" || r.RepositoryID != "" || r.AccountID != "" {
			return compute.Problem("invalid_request", "This action uses only the captured project and association revision.")
		}
	case "review":
		if !githubRepositoryName.MatchString(r.Repository) || r.RepositoryID != "" || r.AccountID != "" {
			return compute.Problem("invalid_request", "Review one GitHub owner/repository without credentials or URL parameters.")
		}
	case "associate":
		if !githubRepositoryName.MatchString(r.Repository) || r.RepositoryID == "" || r.AccountID == "" {
			return compute.Problem("invalid_request", "Associate the explicitly reviewed repository and GitHub account identities.")
		}
	default:
		return compute.Problem("invalid_request", "Unknown project GitHub action.")
	}
	return nil
}

// ProjectGitHub uses the same lifecycle lease and immutable guest scope as
// project files. Reads never submit tasks or allocate durable request receipts.
func (s *Service) ProjectGitHub(ctx context.Context, id, action string, req compute.ProjectGitHubRequest) (any, error) {
	if err := validateProjectGitHub(id, action, req); err != nil {
		return nil, err
	}
	b := req.Binding
	if err := s.requireProjectHost(b.HostID); err != nil {
		return nil, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, b.VolumeID, b.MachineID)
	if err != nil {
		return nil, err
	}
	defer release()
	p, err := s.store.Project(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.HostID != b.HostID || p.VolumeID != b.VolumeID || p.Revision != b.ProjectRevision {
		return nil, compute.Problem("binding_changed", "The GitHub request's original project changed.")
	}
	if p.GitHubRevision != req.Revision {
		return nil, compute.Problem("github_association_changed", "The GitHub association changed. Review its current state explicitly.")
	}
	_, m, err := projectEnvironment(ctx, s.store.db, b.VolumeID, b.MachineID, b.VolumeRevision)
	if err != nil {
		return nil, err
	}
	driver, health, err := s.projectGuest(ctx, m)
	if err != nil {
		return nil, err
	}
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		return nil, err
	}
	if image.Guest == nil || !guestimage.Has(image.Guest.Capabilities, "project-github") || !guestimage.Has(health.Capabilities, "project-github") {
		return nil, compute.Problem("unsupported", "This guest does not support the read-only GitHub project protocol. Explicitly select a compatible rebuilt image.")
	}
	if health.BootID != b.BootID || health.InstanceID != b.InstanceID {
		return nil, compute.Problem("binding_changed", "The GitHub request's original VM boot or guest instance changed.")
	}
	input := protocol.ProjectGitHubRequest{ProjectFileScope: protocol.ProjectFileScope{Root: p.Root, RootIdentity: p.RootIdentity, BootID: b.BootID, InstanceID: b.InstanceID}}
	guestAction := action
	switch action {
	case "clear":
		guestAction = "git"
	case "review", "associate":
		guestAction, input.Repository = "review", req.Repository
	case "refresh":
		if p.GitHub == nil {
			return nil, compute.Problem("github_setup_required", "Explicitly review and associate a repository before refreshing GitHub.")
		}
		if p.GitHub.MachineID != b.MachineID || p.GitHub.Profile != "axv" {
			return nil, compute.Problem("github_connection_changed", "Review the dedicated GitHub profile on this VM before using the retained repository association.")
		}
		guestAction = "snapshot"
		input.RepositoryID, input.AccountID = p.GitHub.Repository.ID, p.GitHub.Account.ID
	}
	var observation protocol.ProjectGitHubObservation
	if err := projectGitHubJSON(ctx, m, driver, guestAction, input, &observation); err != nil {
		return nil, err
	}
	if action != "git" && action != "clear" {
		if !observation.Git.Repository || observation.Repository.ID == "" || !githubRepositoryName.MatchString(observation.Repository.NameWithOwner) || observation.Repository.URL != "https://github.com/"+observation.Repository.NameWithOwner || observation.Account.ID == "" || observation.Account.Login == "" {
			return nil, compute.Problem("guest_identity_mismatch", "The guest returned an incomplete reviewed GitHub identity.")
		}
		if action == "associate" && (observation.Repository.ID != req.RepositoryID || observation.Account.ID != req.AccountID) {
			return nil, compute.Problem("github_connection_changed", "The reviewed GitHub account or repository changed before association.")
		}
		if action == "refresh" && (observation.Repository.ID != input.RepositoryID || observation.Account.ID != input.AccountID) {
			return nil, compute.Problem("github_connection_changed", "The selected GitHub account or repository changed. No result was adopted.")
		}
	}
	// A provider response is an observation, not permission to adopt a newer
	// guest incarnation or a concurrently edited repository association.
	_, after, err := s.projectGuest(ctx, m)
	if err != nil {
		return nil, err
	}
	if after.BootID != b.BootID || after.InstanceID != b.InstanceID {
		return nil, compute.Problem("binding_changed", "The original guest changed while GitHub was read.")
	}
	if action == "associate" || action == "clear" {
		var association *compute.ProjectGitHubAssociation
		if action == "associate" {
			association = &compute.ProjectGitHubAssociation{Repository: observation.Repository, Account: observation.Account, MachineID: b.MachineID, Profile: "axv", UpdatedAt: time.Now().UTC()}
		}
		return s.store.setProjectGitHub(ctx, id, req.Revision, association)
	}
	current, err := s.store.Project(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.GitHubRevision != req.Revision {
		return nil, compute.Problem("github_association_changed", "The GitHub association changed while this observation was being read.")
	}
	return compute.ProjectGitHubResult{Binding: b, Revision: req.Revision, Observation: observation}, nil
}

func (s *Store) setProjectGitHub(ctx context.Context, id string, revision int64, association *compute.ProjectGitHubAssociation) (result compute.ProjectGitHubState, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := project(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.GitHubRevision != revision {
			return compute.Problem("github_association_changed", "The GitHub association was edited elsewhere; no settings were overwritten.")
		}
		p.GitHubRevision++
		p.GitHub = association
		if _, err := tx.ExecContext(ctx, "UPDATE projects SET data=? WHERE id=?", encode(p), id); err != nil {
			return err
		}
		result = githubState(p)
		return nil
	})
	return
}

func projectGitHubJSON(ctx context.Context, m compute.Machine, driver compute.Driver, action string, input protocol.ProjectGitHubRequest, output *protocol.ProjectGitHubObservation) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	token, err := driver.GuestToken(m)
	if err != nil {
		return compute.Problem("guest_unavailable", "Guest authentication material is unavailable.")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	transport := compute.GuestTransport(m, driver)
	defer transport.CloseIdleConnections()
	r, err := http.NewRequestWithContext(ctx, "POST", "http://guest/v1/project-github/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	if err != nil {
		return compute.Problem("guest_unavailable", "The original guest GitHub read could not be completed.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var problem compute.Error
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem); err != nil || problem.Code == "" || problem.Message == "" {
			return compute.Problem("guest_unavailable", "The guest GitHub read failed (HTTP %d).", response.StatusCode)
		}
		return sanitizedGitHubProblem(problem.Code)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || !jsonunicode.Valid(data) {
		return compute.Problem("guest_unavailable", "The guest returned invalid or oversized GitHub data.")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil || d.Decode(new(any)) != io.EOF {
		return compute.Problem("guest_unavailable", "The guest returned invalid GitHub data.")
	}
	return nil
}

func sanitizedGitHubProblem(code string) error {
	messages := map[string]string{
		"github_cli_missing":            "Install a reviewed GitHub CLI in the guest before using this connection.",
		"github_auth_required":          "Sign in explicitly using the dedicated AXV GitHub profile in Guest Terminal.",
		"github_profile_invalid":        "The dedicated guest GitHub profile is unavailable or has unsafe permissions.",
		"github_account_changed":        "The selected GitHub account changed. Review the connection explicitly.",
		"github_repository_changed":     "The reviewed GitHub repository identity changed. No result was adopted.",
		"github_unavailable":            "GitHub could not be reached through the original guest connection.",
		"github_rate_limited":           "GitHub rate-limited this read. Wait before explicitly refreshing again.",
		"github_forbidden":              "The selected GitHub account lacks permission for the requested read.",
		"github_repository_unavailable": "The GitHub repository is missing or inaccessible to the selected account.",
		"github_response_invalid":       "GitHub returned incomplete, invalid or oversized data; no snapshot was adopted.",
		"github_timeout":                "The bounded GitHub read timed out.",
		"github_busy":                   "The guest GitHub read capacity is busy. Refresh later.",
		"github_git_invalid":            "Git metadata is malformed or uses unsafe paths.",
		"github_git_unsupported":        "This repository uses unsupported or oversized Git metadata.",
		"github_git_changed":            "Git metadata changed during the read. Review the current checkout and refresh explicitly.",
		"github_git_unavailable":        "Git metadata could not be read in the original guest project.",
		"git_metadata_invalid":          "The captured Git metadata cannot be used for this read.",
		"invalid_request":               "The guest rejected the typed project GitHub request.",
		"boot_changed":                  "The original VM boot changed.",
		"instance_changed":              "The original guest-service instance changed.",
		"project_changed":               "The original project root identity changed.",
		"project_unavailable":           "The original project volume is unavailable.",
		"unsupported":                   "This guest does not support the requested GitHub read.",
	}
	if message, ok := messages[code]; ok {
		return compute.Problem(code, "%s", message)
	}
	return compute.Problem("guest_unavailable", "The original guest GitHub read could not be completed.")
}
