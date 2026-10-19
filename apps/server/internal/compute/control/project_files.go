package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

func (s *Service) projectGuest(ctx context.Context, m compute.Machine) (compute.Driver, protocol.Health, error) {
	var health protocol.Health
	if m.HostID != s.store.HostID {
		return nil, health, compute.Problem("binding_changed", "The VM belongs to another AXV host.")
	}
	driver := s.drivers[m.Spec.Provider]
	if driver == nil {
		return nil, health, compute.Problem("unsupported", "The project's VM provider is unavailable.")
	}
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		return nil, health, err
	}
	if image.Guest == nil || image.Guest.Compatible() != nil || !guestimage.Has(image.Guest.Capabilities, "project-files") {
		return nil, health, compute.Problem("unsupported", "This image does not declare compatible VM project-file access. Build and explicitly select an image with project-files support.")
	}
	health, err = compute.Health(ctx, m, driver)
	if err != nil {
		return nil, health, compute.Problem("guest_unavailable", "The project VM guest could not be verified: %v", err)
	}
	if !health.Ready || !guestimage.Has(health.Capabilities, "project-files") || health.BootID == "" || health.InstanceID == "" {
		return nil, health, compute.Problem("guest_unavailable", "The guest is not ready for project files or lacks an execution instance identity. %s", health.Reason)
	}
	return driver, health, nil
}

// projectJSON deliberately never retries. A lost write response has an uncertain
// outcome; the caller can read the original binding and compare actual bytes.
func projectJSON(ctx context.Context, m compute.Machine, driver compute.Driver, action string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	token, err := driver.GuestToken(m)
	if err != nil {
		return compute.Problem("guest_unavailable", "The guest authentication material is unavailable.")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	transport := compute.GuestTransport(m, driver)
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, "POST", "http://guest/v1/project-files/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		return compute.Problem("guest_unavailable", "The original guest file request could not be completed. A write may have succeeded; inspect the original binding before retrying.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && !(action == "mkdir" && response.StatusCode == http.StatusCreated) {
		var problem compute.Error
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem); err != nil || problem.Code == "" || problem.Message == "" {
			return compute.Problem("guest_unavailable", "The guest returned an invalid project-file response (HTTP %d).", response.StatusCode)
		}
		return &problem
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || !jsonunicode.Valid(data) {
		return compute.Problem("guest_unavailable", "The guest returned invalid project-file data. A write may have succeeded; inspect the original binding before retrying.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(output); err != nil || decoder.Decode(new(any)) != io.EOF {
		return compute.Problem("guest_unavailable", "The guest returned invalid project-file data. A write may have succeeded; inspect the original binding before retrying.")
	}
	return nil
}

func (s *Service) RegisterProject(ctx context.Context, req compute.RegisterProjectRequest) (compute.Project, error) {
	if err := validateProjectRequest(req); err != nil {
		return compute.Project{}, err
	}
	p, exists, err := projectRequest(ctx, s.store.db, req)
	if err != nil || exists {
		return p, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, req.VolumeID, req.MachineID)
	if err != nil {
		return compute.Project{}, err
	}
	_, m, err := projectEnvironment(ctx, s.store.db, req.VolumeID, req.MachineID, req.VolumeRevision)
	var root protocol.ProjectFileRoot
	if err == nil {
		var driver compute.Driver
		var health protocol.Health
		driver, health, err = s.projectGuest(ctx, m)
		if err == nil {
			err = projectJSON(ctx, m, driver, "inspect", protocol.ProjectFileInspectRequest{Root: req.Root, BootID: health.BootID, InstanceID: health.InstanceID}, &root)
		}
	}
	release()
	if err != nil {
		return compute.Project{}, err
	}
	if root.Root != req.Root || root.RootIdentity == "" || len(root.RootIdentity) > 128 {
		return compute.Project{}, compute.Problem("guest_identity_mismatch", "The guest did not verify the selected project root.")
	}
	// Recheck attachment and operation locks atomically at publication. Guest
	// inspection did not create or modify project data.
	return s.store.registerProject(ctx, req, root.RootIdentity)
}

func (s *Service) ProjectBinding(ctx context.Context, id, hostID string) (compute.ProjectBindingResult, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return compute.ProjectBindingResult{}, err
	}
	p, err := s.store.Project(ctx, id)
	if err != nil {
		return compute.ProjectBindingResult{}, err
	}
	if p.HostID != s.store.HostID {
		return compute.ProjectBindingResult{}, compute.Problem("binding_changed", "The project belongs to another AXV host.")
	}
	v, err := s.store.Volume(ctx, p.VolumeID)
	if err != nil {
		return compute.ProjectBindingResult{}, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, v.ID, v.MachineID)
	if err != nil {
		return compute.ProjectBindingResult{}, err
	}
	defer release()
	_, m, err := projectEnvironment(ctx, s.store.db, v.ID, v.MachineID, v.Revision)
	if err != nil {
		return compute.ProjectBindingResult{}, err
	}
	driver, health, err := s.projectGuest(ctx, m)
	if err != nil {
		return compute.ProjectBindingResult{}, err
	}
	var root protocol.ProjectFileRoot
	scope := protocol.ProjectFileScope{Root: p.Root, RootIdentity: p.RootIdentity, BootID: health.BootID, InstanceID: health.InstanceID}
	if err := projectJSON(ctx, m, driver, "inspect", scope, &root); err != nil {
		return compute.ProjectBindingResult{}, err
	}
	if root.Root != p.Root || root.RootIdentity != p.RootIdentity {
		return compute.ProjectBindingResult{}, compute.Problem("project_changed", "The project's root directory changed. Its identity was not replaced.")
	}
	return compute.ProjectBindingResult{Project: p, Binding: compute.ProjectBinding{HostID: s.store.HostID, ProjectID: p.ID, ProjectRevision: p.Revision, VolumeID: v.ID, VolumeRevision: v.Revision, MachineID: m.ID, BootID: health.BootID, InstanceID: health.InstanceID}}, nil
}

func (s *Service) ProjectFiles(ctx context.Context, id, action string, req compute.ProjectFileRequest) (any, error) {
	b := req.Binding
	values := []string{id, req.Path, req.Cursor, req.Content, b.HostID, b.ProjectID, b.VolumeID, b.MachineID, b.BootID, b.InstanceID}
	if req.ExpectedSHA256 != nil {
		values = append(values, *req.ExpectedSHA256)
	}
	for _, value := range values {
		if !utf8.ValidString(value) {
			return nil, compute.Problem("invalid_request", "Project files require valid UTF-8 without replacing submitted characters.")
		}
	}
	if err := s.requireProjectHost(b.HostID); err != nil {
		return nil, err
	}
	if b.ProjectID != id || b.BootID == "" || b.InstanceID == "" {
		return nil, compute.Problem("binding_changed", "The original complete project binding is required.")
	}
	if action != "list" && action != "read" && action != "write" && action != "mkdir" {
		return nil, compute.Problem("invalid_request", "Unknown project-file action.")
	}
	if (action != "list" && (req.Cursor != "" || req.Limit != 0)) || (action != "write" && (req.Content != "" || req.ExpectedSHA256 != nil)) || (action == "write" && req.ExpectedSHA256 == nil) {
		return nil, compute.Problem("invalid_request", "File parameters must match the requested action; writes require an explicit expectedSha256.")
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
		return nil, compute.Problem("binding_changed", "The project binding changed. This request was not redirected.")
	}
	_, m, err := projectEnvironment(ctx, s.store.db, b.VolumeID, b.MachineID, b.VolumeRevision)
	if err != nil {
		return nil, err
	}
	driver, health, err := s.projectGuest(ctx, m)
	if err != nil {
		return nil, err
	}
	if health.BootID != b.BootID || health.InstanceID != b.InstanceID {
		return nil, compute.Problem("binding_changed", "The VM boot or guest-service instance changed. This request was not redirected.")
	}
	scope := protocol.ProjectFileScope{Root: p.Root, RootIdentity: p.RootIdentity, BootID: b.BootID, InstanceID: b.InstanceID}
	base := compute.ProjectBindingResult{Project: p, Binding: b}
	switch action {
	case "list":
		result := struct {
			compute.ProjectBindingResult
			protocol.ProjectFileListing
		}{ProjectBindingResult: base}
		err = projectJSON(ctx, m, driver, action, protocol.ProjectFileListRequest{ProjectFileScope: scope, Path: req.Path, Cursor: req.Cursor, Limit: req.Limit}, &result.ProjectFileListing)
		return result, err
	case "read", "write":
		result := struct {
			compute.ProjectBindingResult
			protocol.ProjectFile
		}{ProjectBindingResult: base}
		var input any = protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: req.Path}
		if action == "write" {
			input = protocol.ProjectFileWriteRequest{ProjectFileScope: scope, Path: req.Path, Content: req.Content, ExpectedSHA256: req.ExpectedSHA256}
		}
		err = projectJSON(ctx, m, driver, action, input, &result.ProjectFile)
		if err == nil && result.Path != req.Path {
			err = compute.Problem("guest_identity_mismatch", "The guest returned a different project file.")
		}
		return result, err
	case "mkdir":
		result := struct {
			compute.ProjectBindingResult
			protocol.ProjectFileEntry
		}{ProjectBindingResult: base}
		err = projectJSON(ctx, m, driver, action, protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: req.Path}, &result.ProjectFileEntry)
		if err == nil && result.Path != req.Path {
			err = compute.Problem("guest_identity_mismatch", "The guest returned a different project directory.")
		}
		return result, err
	}
	return nil, fmt.Errorf("unreachable project-file action")
}
