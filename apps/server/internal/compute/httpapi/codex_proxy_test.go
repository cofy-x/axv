package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestComputeProxiesGuestCodexRoutesWhenCapabilityDeclared(t *testing.T) {
	service, spec := operationService(t)
	created, err := service.Create(context.Background(), compute.CreateRequest{RequestID: "test-codex-proxy", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := service.Store.Claim(context.Background())
	if err != nil || !found {
		t.Fatalf("claim: %v, %v", found, err)
	}
	if err = service.Store.Finish(context.Background(), op, compute.Observation{Status: "running", Ready: true}, nil); err != nil {
		t.Fatal(err)
	}

	capabilities := []string{"exec", "terminals", "codex"}
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(guestproto.Health{
				Version:      guestproto.Version,
				MachineID:    created.Machine.ID,
				BootID:       "boot-codex",
				Ready:        true,
				Capabilities: capabilities,
			})
			return
		}
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": true, "authMode": "proxy", "fullAutoDefault": true})
	}))
	t.Cleanup(server.Close)
	service.Drivers = map[string]compute.Driver{"lima": accessDriver{address: strings.TrimPrefix(server.URL, "http://")}}
	service.rebuild()

	// Forwarded when "codex" capability is declared.
	req := httptest.NewRequest(http.MethodGet, "/v1/machines/"+created.Machine.ID+"/guest/codex/info", nil)
	req.Header.Set("X-AXV-Host-ID", service.Store.HostID)
	res := httptest.NewRecorder()
	Handler(service.Service).ServeHTTP(res, req)
	if res.Code != http.StatusOK || gotPath != "/v1/codex/info" {
		t.Fatalf("expected 200 and forwarded /v1/codex/info, got %d (path=%q): %s", res.Code, gotPath, res.Body.String())
	}

	// Rejected with 503 unsupported when "codex" capability is absent.
	capabilities = []string{"exec", "terminals"}
	gotPath = ""
	req = httptest.NewRequest(http.MethodGet, "/v1/machines/"+created.Machine.ID+"/guest/codex/info", nil)
	req.Header.Set("X-AXV-Host-ID", service.Store.HostID)
	res = httptest.NewRecorder()
	Handler(service.Service).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"code":"unsupported"`) || gotPath != "" {
		t.Fatalf("expected 503 unsupported without forwarding, got %d (path=%q): %s", res.Code, gotPath, res.Body.String())
	}
}
