package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/githubreview"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/tailnet/access"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestRemoteGitHubReviewAllowsReadsAndWorkflowWhileKeepingConfigLocalOnly(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	terminals := terminal.New()
	defer terminals.Close()
	agents, err := codex.New(store, codex.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer agents.Close()
	agentAPI := codexhttp.New(agents)
	defer agentAPI.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reviews, err := githubreview.Open(ctx, githubreview.Options{
		Directory: filepath.Join(t.TempDir(), "github-review"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reviews.Close()

	application := http.NewServeMux()
	reviews.Register(application)
	shared := newHandler(application, terminals, agentAPI)

	owner := "123"
	middleware, err := access.NewMiddleware(access.MiddlewareOptions{
		Origin:         "https://workstation.example.ts.net",
		OwnerUserID:    "123",
		AllowRoute:     remoteRoute,
		LocalOnlyRoute: remoteLocalOnlyRoute,
		WhoIs: func(context.Context, string) (access.Identity, error) {
			return access.Identity{UserID: owner, NodeID: "peer-node"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer middleware.Close()
	remote := middleware.Wrap(shared)

	call := func(method, path, origin, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://workstation.example.ts.net"+path, strings.NewReader(body))
		r.Host = "workstation.example.ts.net"
		r.RemoteAddr = "100.64.1.2:12345"
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		remote.ServeHTTP(w, r)
		return w
	}

	// Authenticated remote owner can read snapshot (with canConfigure=false) and inbox.
	w := call(http.MethodGet, "/api/github-review?summary=1", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("remote github-review summary denied: %d %s", w.Code, w.Body.String())
	}
	var snapshot struct {
		CanConfigure bool `json:"canConfigure"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || snapshot.CanConfigure {
		t.Fatalf("expected remote snapshot canConfigure=false, got %s", w.Body.String())
	}
	if w := call(http.MethodGet, "/api/github-review/inbox?view=action", "", ""); w.Code != http.StatusOK {
		t.Fatalf("remote github-review inbox denied: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, "/api/github-review/vm-readiness?repository=owner/repo", "", ""); w.Code != http.StatusOK {
		t.Fatalf("remote github-review vm-readiness denied: %d %s", w.Code, w.Body.String())
	}

	// Remote mutations require exact Origin; with exact Origin they reach the service.
	if w := call(http.MethodPost, "/api/github-review/run", "https://other.example.ts.net", `{}`); w.Code != http.StatusForbidden {
		t.Fatalf("remote mutation accepted wrong origin: %d %s", w.Code, w.Body.String())
	}
	// /run reaches the handler and returns 409 github_review_inactive (not 403/404).
	if w := call(http.MethodPost, "/api/github-review/run", "https://workstation.example.ts.net", `{}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "github_review_inactive") {
		t.Fatalf("remote /run did not reach service: %d %s", w.Code, w.Body.String())
	}

	// Machine-local administration routes stay strictly forbidden remotely.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/api/github-review/config"},
		{http.MethodPost, "/api/github-review/connect/github"},
		{http.MethodPost, "/api/github-review/connect/codex"},
		{http.MethodPost, "/api/github-review/disconnect/github"},
		{http.MethodPost, "/api/github-review/disconnect/codex"},
		{http.MethodPost, "/api/github-review/approve-comparison-scenario"},
	} {
		w := call(tc.method, tc.path, "https://workstation.example.ts.net", `{}`)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "remote_local_only") {
			t.Fatalf("local-only route %s %s admitted remotely: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// Non-owner remote user is rejected.
	owner = "456"
	if w := call(http.MethodGet, "/api/github-review?summary=1", "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner remote read admitted: %d %s", w.Code, w.Body.String())
	}
}
