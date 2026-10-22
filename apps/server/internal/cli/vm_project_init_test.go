package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestVMProjectInitializationCLIRequiresOriginalBindingAndIdentity(t *testing.T) {
	file, saved := projectBindingFixture(t)
	var received compute.InitializeProjectRequest
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/projects/project-cli/initialize" || r.Header.Get("X-AXV-Host-ID") != saved.Binding.HostID {
			t.Errorf("initialization changed host/project: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(taskResultFixture(saved.Binding))
	})
	_, _, err := cliInvoke("vm", "project", "initialize", "project-cli", "new-project", "--binding-file", file)
	if err == nil || !strings.Contains(err.Error(), "--request-id") {
		t.Fatal("initialization did not require durable request ID", err)
	}
	out, _, err := cliInvoke("vm", "project", "initialize", "project-cli", "new-project", "--binding-file", file, "--request-id", "initialize-cli-original", "--repository", "https://example.com/repository.git", "--name", "New project")
	if err != nil || received.Binding != saved.Binding || received.RequestID != "initialize-cli-original" || received.Directory != "new-project" || received.Name != "New project" || !strings.Contains(out, "attempt-original") {
		t.Fatalf("initialization request changed: %+v %s %v", received, out, err)
	}
}
