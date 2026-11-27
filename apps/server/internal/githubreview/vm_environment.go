package githubreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const (
	reviewVMRegistryLimit = 64 << 10
	reviewVMRegistryCount = 100
	defaultReviewVMName   = "Default review VM"
)

var (
	reviewVMHostID    = regexp.MustCompile(`^host-[a-f0-9]{32}$`)
	reviewVMProjectID = regexp.MustCompile(`^project-[a-f0-9]{32}$`)
)

type ReviewVMTarget struct {
	Name      string `json:"name"`
	HostID    string `json:"hostId"`
	MachineID string `json:"machineId"`
	ProjectID string `json:"projectId"`
}

type ReviewVMEnvironment struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	HostID     string `json:"hostId,omitempty"`
	MachineID  string `json:"machineId,omitempty"`
	ProjectID  string `json:"projectId,omitempty"`
}

type ReviewVMReadiness struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type reviewVMInspector interface {
	Machine(context.Context, string) (compute.Machine, error)
	ProjectBinding(context.Context, string) (computeclient.ProjectBindingResult, error)
}

func (s *Service) reviewVMReadiness(ctx context.Context, repository string) ReviewVMReadiness {
	vm, ok := s.vmForRepository(repository).(ComputeVMTests)
	if !ok || !vm.comparisonReady() {
		return ReviewVMReadiness{"unconfigured", "Configure a dedicated review VM and project on this workstation."}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, err := vm.Gateway.ConnectHost(ctx, vm.HostID)
	if err != nil {
		return ReviewVMReadiness{"host_unavailable", "Connect AXV to the Compute host registered for this repository."}
	}
	defer client.Close()
	return vm.inspectReadiness(ctx, client)
}

func (v ComputeVMTests) inspectReadiness(ctx context.Context, client reviewVMInspector) ReviewVMReadiness {
	machine, err := client.Machine(ctx, v.MachineID)
	if err != nil || machine.ID != v.MachineID || machine.HostID != v.HostID {
		return ReviewVMReadiness{"machine_unavailable", "Check that the registered review VM exists on the selected Compute host."}
	}
	if machine.Status != "running" || !machine.Ready {
		return ReviewVMReadiness{"guest_unready", "Start the registered review VM and wait for its guest to become ready."}
	}
	if !slices.Contains(machine.GuestCapabilities, "project-tasks") {
		return ReviewVMReadiness{"tasks_unsupported", "Use an AXV guest image with working VM Project Tasks."}
	}
	if machine.Spec.Resources.MemoryMiB < reviewTaskLimits.MemoryMiB || machine.Spec.Resources.CPUs < reviewTaskLimits.CPUs {
		return ReviewVMReadiness{"limits_unavailable", fmt.Sprintf("Allocate at least %d CPU and %d MiB to the review VM, plus memory for its guest.", reviewTaskLimits.CPUs, reviewTaskLimits.MemoryMiB)}
	}
	if _, err := v.readProjectBinding(ctx, client); err != nil {
		return ReviewVMReadiness{"project_unavailable", "Check that the registered review project is accessible on this exact VM."}
	}
	return ReviewVMReadiness{"reachable", "The review VM and project are reachable. The test will check its commit and dependencies when it runs."}
}

func (v ComputeVMTests) readProjectBinding(ctx context.Context, client reviewVMInspector) (compute.ProjectBinding, error) {
	project, err := client.ProjectBinding(ctx, v.ProjectID)
	if err != nil {
		return compute.ProjectBinding{}, err
	}
	b := project.Binding
	if !completeComparisonBinding(&b) || b.HostID != v.HostID || b.MachineID != v.MachineID || b.ProjectID != v.ProjectID ||
		b.ProjectRevision < 1 || b.VolumeRevision < 1 || b.VolumeID == "" {
		return compute.ProjectBinding{}, errComparisonVM
	}
	return b, nil
}

func (target *ReviewVMTarget) UnmarshalJSON(data []byte) error {
	return decodeFields(data, map[string]any{
		"name": &target.Name, "hostId": &target.HostID, "machineId": &target.MachineID, "projectId": &target.ProjectID,
	})
}

// LoadReviewVMConfig reads a local operator-owned repository-to-VM registry.
// It does not read anything from the checkout or change the default binding.
func LoadReviewVMConfig(filename string) (map[string]ReviewVMTarget, error) {
	if filename == "" {
		return nil, nil
	}
	info, err := os.Stat(filename)
	if err != nil {
		return nil, fmt.Errorf("read AXV_REVIEW_VM_CONFIG: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > reviewVMRegistryLimit {
		return nil, errors.New("AXV_REVIEW_VM_CONFIG must be a regular file of at most 64 KiB")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open AXV_REVIEW_VM_CONFIG: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, reviewVMRegistryLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read AXV_REVIEW_VM_CONFIG: %w", err)
	}
	if len(data) > reviewVMRegistryLimit || !jsonunicode.Valid(data) {
		return nil, errors.New("AXV_REVIEW_VM_CONFIG requires valid UTF-8 JSON of at most 64 KiB")
	}
	targets, err := parseReviewVMConfig(data)
	if err != nil {
		return nil, fmt.Errorf("invalid AXV_REVIEW_VM_CONFIG: %w", err)
	}
	return targets, nil
}

func parseReviewVMConfig(data []byte) (map[string]ReviewVMTarget, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if start, err := d.Token(); err != nil || start != json.Delim('{') {
		return nil, errors.New("expected an object keyed by owner/repository")
	}
	targets := make(map[string]ReviewVMTarget)
	for d.More() {
		token, err := d.Token()
		repository, ok := token.(string)
		if err != nil || !ok || !validRepository(repository) {
			return nil, errors.New("each review VM key must be an exact owner/repository")
		}
		key := strings.ToLower(repository)
		if _, exists := targets[key]; exists || len(targets) == reviewVMRegistryCount {
			return nil, errors.New("expected at most 100 case-insensitively unique review VM repository keys")
		}
		var target ReviewVMTarget
		if err := d.Decode(&target); err != nil || !target.valid() {
			return nil, fmt.Errorf("repository %s requires a name and full hostId, machineId and projectId", repository)
		}
		targets[key] = target
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, errors.New("expected the end of the repository object")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("expected only one repository object")
	}
	return targets, nil
}

func (target ReviewVMTarget) valid() bool {
	return target.Name != "" && len(target.Name) <= 80 && strings.TrimSpace(target.Name) == target.Name &&
		strings.IndexFunc(target.Name, unicode.IsControl) < 0 &&
		reviewVMHostID.MatchString(target.HostID) && compute.IsMachineID(target.MachineID) && reviewVMProjectID.MatchString(target.ProjectID)
}

func (v ComputeVMTests) forRepositoryWithSettings(repository string, saved map[string]ReviewVMTarget) (ComputeVMTests, ReviewVMEnvironment) {
	key := strings.ToLower(repository)
	if target, exists := v.RepositoryTargets[key]; exists {
		v.RepositoryTargets = nil
		v.HostID, v.MachineID, v.ProjectID = target.HostID, target.MachineID, target.ProjectID
		return v, ReviewVMEnvironment{
			Name:       target.Name,
			Configured: v.comparisonReady(),
			Source:     "env",
			HostID:     target.HostID,
			MachineID:  target.MachineID,
			ProjectID:  target.ProjectID,
		}
	}
	if target, exists := saved[key]; exists {
		v.RepositoryTargets = nil
		v.HostID, v.MachineID, v.ProjectID = target.HostID, target.MachineID, target.ProjectID
		return v, ReviewVMEnvironment{
			Name:       target.Name,
			Configured: v.comparisonReady(),
			Source:     "settings",
			HostID:     target.HostID,
			MachineID:  target.MachineID,
			ProjectID:  target.ProjectID,
		}
	}
	v.RepositoryTargets = nil
	configured := v.comparisonReady()
	env := ReviewVMEnvironment{Name: defaultReviewVMName, Configured: configured, Source: "default"}
	if configured {
		env.HostID, env.MachineID, env.ProjectID = v.HostID, v.MachineID, v.ProjectID
	}
	return v, env
}

func (s *Service) vmForRepository(repository string) VMTestRunner {
	if v, ok := s.vm.(ComputeVMTests); ok {
		scoped, _ := v.forRepositoryWithSettings(repository, s.savedVMTargets())
		return scoped
	}
	return s.vm
}

func reviewVMConfigured(vm VMTestRunner) bool {
	if v, ok := vm.(interface{ comparisonReady() bool }); ok {
		return v.comparisonReady()
	}
	return vm != nil
}

func (s *Service) scenarioVMReady(repository string) bool {
	vm := s.vmForRepository(repository)
	_, ok := vm.(scenarioBindingProvider)
	return ok && reviewVMConfigured(vm)
}

func (s *Service) repositoryEnvironments(repositories []string) map[string]ReviewVMEnvironment {
	vm, ok := s.vm.(ComputeVMTests)
	saved := s.savedVMTargets()
	if !ok || (len(repositories) == 0 && len(vm.RepositoryTargets) == 0 && len(saved) == 0) {
		return nil
	}
	result := make(map[string]ReviewVMEnvironment, len(repositories)+len(vm.RepositoryTargets)+len(saved))
	watched := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		_, env := vm.forRepositoryWithSettings(repository, saved)
		result[repository] = env
		watched[strings.ToLower(repository)] = true
	}
	for repository := range vm.RepositoryTargets {
		if !watched[repository] {
			_, env := vm.forRepositoryWithSettings(repository, saved)
			result[repository] = env
			watched[repository] = true
		}
	}
	for repository := range saved {
		if !watched[repository] {
			_, env := vm.forRepositoryWithSettings(repository, saved)
			result[repository] = env
		}
	}
	return result
}
