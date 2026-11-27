package githubreview

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
)

type fixedVMReadiness struct {
	machine compute.Machine
	project compute.ProjectBindingResult
}

func (f fixedVMReadiness) Machine(context.Context, string) (compute.Machine, error) {
	return f.machine, nil
}

func (f fixedVMReadiness) ProjectBinding(context.Context, string) (compute.ProjectBindingResult, error) {
	return f.project, nil
}

func TestVMReadinessChecksTheRegisteredRunningProjectAndTaskLimits(t *testing.T) {
	vm := ComputeVMTests{HostID: "host-review", MachineID: "vm-review", ProjectID: "project-review"}
	fixture := fixedVMReadiness{
		machine: compute.Machine{ID: vm.MachineID, HostID: vm.HostID, Status: "running", Ready: true, GuestCapabilities: []string{"project-tasks"},
			Spec: compute.Spec{Resources: compute.Resources{CPUs: 1, MemoryMiB: 1024}}},
		project: compute.ProjectBindingResult{Binding: compute.ProjectBinding{HostID: vm.HostID, ProjectID: vm.ProjectID, MachineID: vm.MachineID,
			ProjectRevision: 1, VolumeRevision: 1, VolumeID: "volume-review", BootID: "boot-review", InstanceID: "instance-review"}},
	}
	for _, check := range []struct {
		want   string
		change func(*fixedVMReadiness)
	}{
		{"reachable", func(*fixedVMReadiness) {}},
		{"machine_unavailable", func(f *fixedVMReadiness) { f.machine.ID = "other" }},
		{"guest_unready", func(f *fixedVMReadiness) { f.machine.Ready = false }},
		{"tasks_unsupported", func(f *fixedVMReadiness) { f.machine.GuestCapabilities = nil }},
		{"limits_unavailable", func(f *fixedVMReadiness) { f.machine.Spec.Resources.MemoryMiB = 512 }},
		{"project_unavailable", func(f *fixedVMReadiness) { f.project.Binding.MachineID = "other" }},
	} {
		t.Run(check.want, func(t *testing.T) {
			current := fixture
			check.change(&current)
			if got := vm.inspectReadiness(t.Context(), current); got.Status != check.want || got.Message == "" {
				t.Fatalf("expected %s, got %+v", check.want, got)
			}
		})
	}
}

func TestVMReadinessRouteIsLocalAndAcceptsOneRepository(t *testing.T) {
	service := Service{}
	mux := http.NewServeMux()
	service.Register(mux)
	const path = route + "/vm-readiness"
	const valid = path + "?repository=owner/repo"
	const loopback = "127.0.0.1:39282"
	for _, check := range []struct {
		path   string
		remote string
		want   int
	}{
		{path, loopback, http.StatusBadRequest},
		{valid + "&repository=owner/repo", loopback, http.StatusBadRequest},
		{valid + "&revision=1", loopback, http.StatusBadRequest},
		{valid, "192.0.2.10:39282", http.StatusForbidden},
		{valid, loopback, http.StatusOK},
	} {
		response := reviewRequest(t, mux, "GET", check.path, "", check.remote, "")
		if response.Code != check.want || (check.want == http.StatusOK && !strings.Contains(response.Body.String(), `"status":"unconfigured"`)) {
			t.Fatalf("%s returned %d, want %d: %s", check.path, response.Code, check.want, response.Body)
		}
	}
}

func TestVMSetupFailureUsesOnlyVerifiedCheckoutToDiagnoseTheStage(t *testing.T) {
	exit := 1
	failed := compute.TaskAttempt{GuestFinal: true, ExitCode: &exit, Reason: "exit status 1"}
	for _, check := range []struct {
		run  VMTestAttempt
		want string
	}{
		{VMTestAttempt{Log: "Git could not resolve github.com"}, "could not prepare the checkout"},
		{VMTestAttempt{Log: "A command could not start", CheckoutSHA: strings.Repeat("a", 40)}, "test command could not start"},
		{VMTestAttempt{Log: "Incomplete log", Truncated: true}, "could not complete test setup or execution"},
		{VMTestAttempt{}, "could not complete test setup or execution"},
	} {
		if got := vmSetupFailure(check.run, failed); !strings.Contains(got, check.want) {
			t.Fatalf("expected %q in %q", check.want, got)
		}
	}
}

func TestReviewVMConfigRequiresExactUniqueRepositoryAndCompleteIDs(t *testing.T) {
	target := ReviewVMTarget{
		Name: "Python checks", HostID: compute.NewID("host"), MachineID: compute.NewID("vm"), ProjectID: compute.NewID("project"),
	}
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	entry := string(data)
	record := func(key, value string) string { return `"` + key + `":` + value }
	dir := t.TempDir()
	file := filepath.Join(dir, "review-vms.json")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("{" + record("Owner/Library", entry) + "}")
	loaded, err := LoadReviewVMConfig(file)
	if err != nil || len(loaded) != 1 || loaded["owner/library"] != target {
		t.Fatalf("configured target lost its canonical key or value: %+v, %v", loaded, err)
	}
	for label, raw := range map[string]string{
		"case duplicate":   "{" + record("Owner/Library", entry) + "," + record("owner/library", entry) + "}",
		"wildcard":         "{" + record("Owner/*", entry) + "}",
		"incomplete":       "{" + record("Owner/Library", `{"name":"Python checks"}`) + "}",
		"null":             "{" + record("Owner/Library", "null") + "}",
		"blank name":       "{" + record("Owner/Library", strings.Replace(entry, "Python checks", " ", 1)) + "}",
		"unknown field":    "{" + record("Owner/Library", strings.TrimSuffix(entry, "}")+`,"source":"checkout"}`) + "}",
		"duplicate field":  "{" + record("Owner/Library", strings.TrimSuffix(entry, "}")+`,"name":"duplicate"}`) + "}",
		"wrong machine id": "{" + record("Owner/Library", strings.Replace(entry, target.MachineID, "development-vm", 1)) + "}",
		"trailing object":  "{" + record("Owner/Library", entry) + "} {}",
		"too large":        strings.Repeat(" ", reviewVMRegistryLimit+1),
	} {
		t.Run(label, func(t *testing.T) {
			write(raw)
			if _, err := LoadReviewVMConfig(file); err == nil {
				t.Fatal("malformed registry was accepted")
			}
		})
	}
	if _, err := LoadReviewVMConfig(dir); err == nil {
		t.Fatal("unexpectedly read a directory as a file")
	}
	if targets, err := LoadReviewVMConfig(""); err != nil || targets != nil {
		t.Fatalf("empty path should leave the default alone: %+v, %v", targets, err)
	}
}

func TestReviewVMSelectionKeepsOverrideAndDefaultReadinessSeparate(t *testing.T) {
	adapter := &gateway.Gateway{}
	primary := ReviewVMTarget{Name: "Python checks", HostID: "other-host", MachineID: "python-vm", ProjectID: "python-project"}
	secondary := ReviewVMTarget{Name: "JavaScript checks", HostID: "same-host", MachineID: "js-vm", ProjectID: "js-project"}
	vm := ComputeVMTests{
		Gateway: adapter, HostID: "default-host", MachineID: "default-vm", ProjectID: "default-project",
		RepositoryTargets: map[string]ReviewVMTarget{"owner/python": primary, "owner/javascript": secondary},
	}
	service := Service{vm: vm}
	envs := service.repositoryEnvironments([]string{"Owner/Python", "owner/pure-go"})
	wantPython := ReviewVMEnvironment{Name: primary.Name, Configured: true, Source: "env", HostID: primary.HostID, MachineID: primary.MachineID, ProjectID: primary.ProjectID}
	wantJS := ReviewVMEnvironment{Name: secondary.Name, Configured: true, Source: "env", HostID: secondary.HostID, MachineID: secondary.MachineID, ProjectID: secondary.ProjectID}
	wantGo := ReviewVMEnvironment{Name: defaultReviewVMName, Configured: true, Source: "default", HostID: vm.HostID, MachineID: vm.MachineID, ProjectID: vm.ProjectID}
	if len(envs) != 3 || envs["Owner/Python"] != wantPython || envs["owner/javascript"] != wantJS || envs["owner/pure-go"] != wantGo {
		t.Fatalf("repository readiness statuses: %+v", envs)
	}
	selected := service.vmForRepository("OWNER/PYTHON").(ComputeVMTests)
	if selected.Gateway != adapter || selected.HostID != primary.HostID || selected.MachineID != primary.MachineID || selected.ProjectID != primary.ProjectID {
		t.Fatalf("override silently selected the default: %+v", selected)
	}
	selected = service.vmForRepository("owner/pure-go").(ComputeVMTests)
	if selected.HostID != vm.HostID || selected.MachineID != vm.MachineID || selected.ProjectID != vm.ProjectID {
		t.Fatalf("unmapped repository lost default: %+v", selected)
	}
	vm.ProjectID = ""
	service.vm = vm
	if service.comparisonReady() || service.scenarioVMReady("owner/pure-go") || !service.scenarioVMReady("OWNER/PYTHON") {
		t.Fatal("a disabled default prevented a registered repository from running")
	}
}
