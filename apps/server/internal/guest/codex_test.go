package guest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex/vmproxy"
)

func TestGuestCodexInfoAndCapabilityGating(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	// Without "codex" capability, /v1/codex/info returns 501 unsupported.
	disabled := testService(t, "exec", "terminals")
	if w := guestRequest(disabled, http.MethodGet, "/v1/codex/info", ""); w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), `"code":"unsupported"`) {
		t.Fatalf("expected 501 unsupported when codex capability absent, got %d: %s", w.Code, w.Body.String())
	}

	// With "codex" capability, /v1/codex/info succeeds and reports defaults.
	s := testService(t, "exec", "terminals", "codex")
	defer s.codex.close()
	w := guestRequest(s, http.MethodGet, "/v1/codex/info", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/codex/info = %d: %s", w.Code, w.Body.String())
	}
	var info guestCodexInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode guestCodexInfo: %v", err)
	}
	if !info.FullAutoDefault {
		t.Fatalf("expected FullAutoDefault=true")
	}
	if info.AuthMode != "none" {
		t.Fatalf("expected authMode=none, got %q", info.AuthMode)
	}
	if info.Install.State != "idle" {
		t.Fatalf("expected install.state=idle, got %q", info.Install.State)
	}

	// Writing ~/.codex/auth.json reports authMode=oauth.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"token":"demo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w = guestRequest(s, http.MethodGet, "/v1/codex/info", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/codex/info (oauth) = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.AuthMode != "oauth" {
		t.Fatalf("expected authMode=oauth, got %q", info.AuthMode)
	}
}

func TestGuestCodexProxyBridgeDetectsHostProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-real-host-secret" {
			http.Error(w, "wrong auth", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	// Start host-side zero-credential proxy on a short Unix socket.
	sockDir, err := os.MkdirTemp("/tmp", "axv-vmproxy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "proxy.sock")
	handler, err := vmproxy.NewHandler(vmproxy.Config{
		APIKey:                "sk-real-host-secret",
		BaseURL:               upstream.URL,
		AllowLoopbackUpstream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := vmproxy.ServeUnix(ctx, sockPath, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	t.Setenv("AXV_GUEST_CODEX_PROXY_SOCKET", sockPath)

	s := testService(t, "exec", "terminals", "codex")
	defer s.codex.close()
	w := guestRequest(s, http.MethodGet, "/v1/codex/info", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/codex/info = %d: %s", w.Code, w.Body.String())
	}
	var info guestCodexInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.HostProxyAvailable || info.AuthMode != "proxy" {
		t.Fatalf("expected hostProxyAvailable=true and authMode=proxy, got %+v", info)
	}
}

func TestGuestCodexCreateSessionConfinesProjectPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := testService(t, "exec", "terminals", "codex")
	defer s.codex.close()

	w := guestRequest(s, http.MethodPost, "/v1/codex/sessions", `{"projectPath":"../../etc"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_directory") {
		t.Fatalf("expected invalid_directory 400 for path traversal, got %d: %s", w.Code, w.Body.String())
	}
}
