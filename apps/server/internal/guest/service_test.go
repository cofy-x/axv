package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func testService(t *testing.T, capabilities ...string) *service {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newService(context.Background(), protocol.Config{
		MachineID: "vm-test", Token: strings.Repeat("a", 32), ProjectRoot: root,
	}, "boot-test", capabilities, func() error { return nil })
	t.Cleanup(s.close)
	return s
}

func guestRequest(s *service, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func assertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v: %s", err, w.Body)
	}
	if w.Code != status || problem.Code != code || problem.Error == "" {
		t.Fatalf("status/code = %d/%s, want %d/%s: %s", w.Code, problem.Code, status, code, w.Body)
	}
}

func TestAuthenticationPrecedesCapabilityAndRouting(t *testing.T) {
	s := testService(t)
	for _, path := range []string{"/v1/health", "/v1/exec", "/unknown"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer different-token")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		assertProblem(t, w, http.StatusForbidden, "forbidden")
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("cache control = %q", got)
		}
	}
	for _, path := range []string{"/v1/exec", "/v1/terminals", "/v1/ssh", "/v1/network"} {
		assertProblem(t, guestRequest(s, "GET", path, ""), http.StatusNotImplemented, "unsupported")
	}
	if got := guestRequest(s, "GET", "/unknown", "").Code; got != http.StatusNotFound {
		t.Fatalf("unknown authenticated route = %d", got)
	}
}

func TestHealthReportsInjectedIdentityReadinessAndCapabilities(t *testing.T) {
	s := testService(t, "network", "exec", "future-feature")
	for _, ready := range []bool{true, false} {
		s.projectReady = func() error {
			if ready {
				return nil
			}
			return errors.New("project volume is not mounted")
		}
		w := guestRequest(s, "GET", "/v1/health", "")
		var health protocol.Health
		if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || health.Version != protocol.Version || health.MachineID != s.config.MachineID || health.BootID != s.bootID || health.Ready != ready {
			t.Fatalf("unexpected health: %s", w.Body)
		}
		if strings.Join(health.Capabilities, ",") != "exec,network" || health.SSHAvailable || (!ready && health.Reason == "") {
			t.Fatalf("unexpected capabilities or readiness reason: %s", w.Body)
		}
	}
}

func TestDirectoryRequiresReadyProjectAndResolvesSymlinks(t *testing.T) {
	s := testService(t)
	inside := filepath.Join(s.config.ProjectRoot, "inside")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(s.config.ProjectRoot, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.config.ProjectRoot, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.config.ProjectRoot, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"", ".", "inside", "inside-link"} {
		got, err := s.directory(relative)
		if err != nil || (got != s.config.ProjectRoot && got != inside) {
			t.Fatalf("directory(%q) = %q, %v", relative, got, err)
		}
	}
	for _, relative := range []string{outside, "..", "outside-link", "missing", "file"} {
		if _, err := s.directory(relative); err == nil {
			t.Fatalf("directory(%q) unexpectedly accepted", relative)
		}
	}
	s.projectReady = func() error { return errors.New("project unavailable") }
	if _, err := s.directory(""); err == nil || err.Error() != "project unavailable" {
		t.Fatalf("readiness failure = %v", err)
	}
}

func TestExecRejectsInvalidRequestsAndFullCapacity(t *testing.T) {
	s := testService(t, "exec")
	tooMany, err := json.Marshal(protocol.ExecRequest{Argv: make([]string, 129)})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{}`, `{"argv":["command"],"timeoutSeconds":-1}`, `{"argv":["command"],"timeoutSeconds":301}`,
		`{"argv":["command"],"unknown":true}`, `{"argv":["command"]} {}`, string(tooMany),
		`{"argv":["` + strings.Repeat("a", 64<<10) + `"]}`,
	} {
		assertProblem(t, guestRequest(s, "POST", "/v1/exec", body), 400, "invalid_request")
	}
	assertProblem(t, guestRequest(s, "POST", "/v1/exec", `{"argv":["command"],"directory":".."}`), 400, "invalid_directory")
	for range cap(s.executions) {
		s.executions <- struct{}{}
	}
	assertProblem(t, guestRequest(s, "POST", "/v1/exec", `{"argv":["command"]}`), 503, "execution_limit")
	for range cap(s.executions) {
		<-s.executions
	}
}

func TestConfigReaderPreservesBootstrapDecodeContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guest.json")
	// Bootstrap configuration historically accepts unknown fields and decodes the
	// first value; stricter request-body rules belong to the HTTP boundary.
	if err := os.WriteFile(path, []byte(`{"machineId":"vm-test","token":"secret","projectRoot":"/projects","future":true} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := readConfig(path)
	if err != nil || config.MachineID != "vm-test" || config.ProjectRoot != "/projects" {
		t.Fatalf("config = %#v, %v", config, err)
	}
}

func TestServeUsesSuppliedListenerAndStopsOnCancellation(t *testing.T) {
	s := testService(t, "exec")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- s.serve(listener, readyWriter{ready}) }()
	select {
	case line := <-ready:
		if line != "AXV_GUEST_READY vm-test\n" {
			t.Fatalf("readiness line = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not publish readiness")
	}
	r, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+listener.Addr().String()+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	if response.StatusCode != 200 {
		t.Fatalf("served health status = %d", response.StatusCode)
	}
	s.cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("service did not stop")
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("service did not close supplied listener")
	}
}

type readyWriter struct{ ready chan<- string }

func (w readyWriter) Write(p []byte) (int, error) {
	w.ready <- string(bytes.Clone(p))
	return len(p), nil
}
