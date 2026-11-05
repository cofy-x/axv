//go:build darwin || linux

package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type managerHTTPServer struct {
	*httptest.Server
	databaseID string
}

func realManager(t *testing.T) (*Manager, *managerHTTPServer, string) {
	t.Helper()
	tmuxPath := executable("tmux")
	if tmuxPath == "" {
		t.Skip("real tmux integration requires tmux")
	}
	home, err := os.MkdirTemp("", "axv-")
	if err != nil {
		t.Fatal(err)
	}
	drained := false
	t.Cleanup(func() {
		if err := removeFixtureDirectory(home, drained); err != nil {
			if !drained {
				t.Log(err)
			} else {
				t.Errorf("remove completed Codex fixture: %v", err)
			}
		}
	})
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "config"))
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
 --version) printf 'codex-cli 0.162.0\n'; exit 0 ;;
 --help) printf '%s\n' '--no-daemon --no-alt-screen --cd --remote app-server'; exit 0 ;;
esac
stty -echo
printf 'FIXTURE_READY PID=%s\n' "$$"
while IFS= read -r line; do
 case "$line" in
  exit) printf 'FIXTURE_BYE\n'; exit 0 ;;
  fail) printf 'FIXTURE_FAILED\n'; exit 7 ;;
  size) printf 'SIZE='; stty size ;;
  env) printf 'ENV=%s OLD=%s CODEX_HOME=%s\n' "$AXV_TEST_ENV" "$AXV_TEST_OLD" "$CODEX_HOME" ;;
  state) printf '%s\000%s\000' "$(pwd -P)" "$AXV_TEST_ENV" > "$AXV_TEST_STATE_FILE"; printf 'STATE_WRITTEN\n' ;;
  scroll) i=0; while [ "$i" -lt 70 ]; do printf 'HISTORY_%s\n' "$i"; i=$((i+1)); done ;;
  *) printf 'ACK=%s\n' "$line" ;;
 esac
done
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := testManagerWithPaths(t, filepath.Join(home, "data"), home)
	writers := newFixtureWriters(t, filepath.Join(home, "fixture-writers"))
	m.probe = func(ctx context.Context) Info {
		return probeUsing(ctx, runtime.GOOS, func(name string) (string, bool) {
			if path := executable(name); path != "" {
				return path, true
			}
			return unavailableExecutable(name), false
		}, func(ctx context.Context, path string, args ...string) (string, error) {
			started := time.Now()
			output, err := command(ctx, path, args...)
			if err != nil {
				cwd, _ := os.Getwd()
				t.Logf("fixture probe failed: executable=%q args=%q elapsed=%s error=%v cwd=%q TMPDIR=%q GOTMPDIR=%q", path, args, time.Since(started), err, cwd, os.Getenv("TMPDIR"), os.Getenv("GOTMPDIR"))
			}
			return output, err
		})
	}
	t.Setenv("AXV_TEST_RUNNER", "1")
	t.Setenv("AXV_TEST_RUNNER_GATE_REQUIRED", "1")
	m.runnerCommand = func(manifest string) ([]string, error) {
		gate, err := writers.prepare(manifest)
		if err != nil {
			return nil, err
		}
		path, err := os.Executable()
		return []string{path, "-test.run=^TestCodexRunnerFixtureProcess$", "--", "--fixture-gate=" + gate, manifest}, err
	}
	m.verifyHistory = func(context.Context, string, string, string, []string, string) error { return nil }
	t.Cleanup(func() {
		// Stop admitting launches before closing this fixture's independent server.
		m.Close()
		output, err := command(context.Background(), tmuxPath, "-L", m.socket, "kill-server")
		if err != nil && !missingRuntime(output) {
			t.Errorf("stop owned Codex fixture tmux server: %v: %s", err, output)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		if err := writers.close(ctx); err != nil {
			t.Errorf("Codex fixture writers did not complete; retained %s: %v", home, err)
			return
		}
		drained = err == nil || missingRuntime(output)
	})
	server := serveManager(t, m)
	return m, server, home
}

func serveManager(t *testing.T, m *Manager) *managerHTTPServer {
	t.Helper()
	mux := http.NewServeMux()
	m.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &managerHTTPServer{Server: server, databaseID: m.databaseID}
}

func jsonRequest(t *testing.T, server *managerHTTPServer, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", server.URL)
	req.Header.Set("X-AXV-Database-ID", server.databaseID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func createSession(t *testing.T, server *managerHTTPServer, request, project string) storage.CodexSession {
	t.Helper()
	status, data := jsonRequest(t, server, "POST", "/api/host/codex/sessions", map[string]any{"requestId": request, "projectPath": project})
	var result struct {
		Session storage.CodexSession `json:"session"`
	}
	if err := json.Unmarshal(data, &result); err != nil || status != 200 || (result.Session.Status != "running" && result.Session.Status != "starting") {
		t.Fatalf("create HTTP %d: %s (%v)", status, data, err)
	}
	if result.Session.Status == "starting" {
		eventually(t, func() bool {
			status, data := jsonRequest(t, server, "GET", runPath(result.Session), nil)
			if status != 200 || json.Unmarshal(data, &result) != nil {
				return false
			}
			return result.Session.Status == "running"
		})
	}
	return result.Session
}

func runPath(session storage.CodexSession) string {
	return "/api/host/codex/sessions/" + session.ID + "/runs/" + session.RunID
}

func connectSession(t *testing.T, server *managerHTTPServer, session storage.CodexSession) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+runPath(session)+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}, "X-AXV-Database-ID": []string{server.databaseID}}})
	if err != nil {
		t.Fatalf("attach: %v response=%+v", err, response)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(data) != `{"ready":true}` {
		t.Fatalf("ready: %s %v", data, err)
	}
	return conn
}

func input(t *testing.T, conn *websocket.Conn, data string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(data)); err != nil {
		t.Fatal(err)
	}
}

func outputUntil(t *testing.T, conn *websocket.Conn, marker string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var result strings.Builder
	for !strings.Contains(result.String(), marker) {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v output=%q", marker, err, result.String())
		}
		if kind == websocket.MessageBinary {
			result.Write(data)
		} else if strings.Contains(string(data), `"error"`) {
			t.Fatalf("control error %s", data)
		}
		if result.Len() > 2<<20 {
			t.Fatal("excessive terminal output")
		}
	}
	return result.String()
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("condition did not complete")
}

func TestRealCodexDetachRestartAndExplicitEnd(t *testing.T) {
	m, server, home := realManager(t)
	session := createSession(t, server, "persistent", home)
	if again := createSession(t, server, "persistent", home); again.ID != session.ID {
		t.Fatal("idempotent creation changed identity")
	}
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	input(t, conn, "first-marker\r")
	outputUntil(t, conn, "ACK=first-marker")
	_ = conn.CloseNow()
	eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.connections[session.RunID]) == 0 })
	pidBefore, err := m.tmux(context.Background(), executable("tmux"), "display-message", "-p", "-t", paneTarget(session), "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	server.Close()
	// Recreate the manager against the same open database as a service restart.
	second, err := New(m.store, m.settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	restarted := serveManager(t, second)
	conn = connectSession(t, restarted, session)
	outputUntil(t, conn, "ACK=first-marker")
	input(t, conn, "after-restart\r")
	outputUntil(t, conn, "ACK=after-restart")
	pidAfter, err := second.tmux(context.Background(), executable("tmux"), "display-message", "-p", "-t", paneTarget(session), "#{pane_pid}")
	if err != nil || pidAfter != pidBefore {
		t.Fatalf("process identity changed %s -> %s (%v)", pidBefore, pidAfter, err)
	}
	for range 2 {
		status, data := jsonRequest(t, restarted, "POST", runPath(session)+"/end", nil)
		if status != 200 || !strings.Contains(string(data), `"status":"ended"`) {
			t.Fatalf("end %d %s", status, data)
		}
	}
	if _, err := second.tmux(context.Background(), executable("tmux"), "has-session", "-t", target(session)); err == nil {
		t.Fatal("explicit end retained process")
	}
}

func TestRealCodexPathsEnvironmentScrollAndNaturalExit(t *testing.T) {
	m, server, home := realManager(t)
	project := filepath.Join(home, "project 'quote $dollar #{session_name};")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AXV_TEST_ENV", "first")
	t.Setenv("AXV_TEST_OLD", "removed")
	stateFile := filepath.Join(home, "literal-state")
	t.Setenv("AXV_TEST_STATE_FILE", stateFile)
	session := createSession(t, server, "special-path", project)
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	input(t, conn, "state\r")
	outputUntil(t, conn, "STATE_WRITTEN")
	// tmux 3.4 escapes display-message output. Verify the child process's
	// actual working directory instead of treating display text as raw data.
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(stateFile); err != nil || string(actual) != canonical+"\x00first\x00" {
		t.Fatalf("child working directory or environment changed: %q (%v)", actual, err)
	}
	input(t, conn, "scroll\r")
	outputUntil(t, conn, "HISTORY_69")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"scroll":20}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		value, _ := m.tmux(context.Background(), executable("tmux"), "display-message", "-p", "-t", paneTarget(session), "#{pane_in_mode}")
		return value == "1"
	})
	other := connectSession(t, server, session)
	outputUntil(t, other, "HISTORY_69")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"scroll":10}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { value, _ := m.copyModes.Load(session.RunID); return value == true })
	input(t, other, "other-client\r")
	outputUntil(t, other, "ACK=other-client")
	_ = conn.CloseNow()
	_ = other.CloseNow()
	conn = connectSession(t, server, session)
	outputUntil(t, conn, "ACK=other-client")
	input(t, conn, "after-scroll\r")
	outputUntil(t, conn, "ACK=after-scroll")
	input(t, conn, "fail\r")
	t.Cleanup(func() {
		if t.Failed() {
			details, err := m.sessionTmux(context.Background(), executable("tmux"), session, "display-message", "-p", "-t", paneTarget(session), "#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}:#{pane_pid}")
			t.Logf("tmux exit details: %q %v", details, err)
		}
	})
	eventually(t, func() bool {
		status, data := jsonRequest(t, server, "GET", "/api/host/codex/sessions/"+session.ID, nil)
		return status == 200 && strings.Contains(string(data), `"status":"failed"`) && strings.Contains(string(data), "status 7")
	})
	if _, err := m.tmux(context.Background(), executable("tmux"), "has-session", "-t", target(session)); err == nil {
		t.Fatal("natural exit leaked dead tmux pane")
	}
	// Keep one session alive so the next launch reuses the same tmux daemon.
	keep := createSession(t, server, "keep-daemon", home)
	_ = keep
	t.Setenv("AXV_TEST_ENV", "second;")
	if err := os.Unsetenv("AXV_TEST_OLD"); err != nil {
		t.Fatal(err)
	}
	newSession := createSession(t, server, "fresh-env", home)
	newConn := connectSession(t, server, newSession)
	outputUntil(t, newConn, "FIXTURE_READY")
	input(t, newConn, "env\r")
	outputUntil(t, newConn, "ENV=second; OLD= CODEX_HOME=")
}

func TestRealCodexLimitRecoveryAndMissingRuntime(t *testing.T) {
	m, server, home := realManager(t)
	var first storage.CodexSession
	for i := 0; i < 4; i++ {
		session := createSession(t, server, fmt.Sprintf("limit-%d", i), home)
		if i == 0 {
			first = session
		}
	}
	status, data := jsonRequest(t, server, "PATCH", "/api/host/codex/sessions/"+first.ID, map[string]any{"revision": 1, "archived": true})
	if status != 200 || !strings.Contains(string(data), `"archivedAt"`) || !strings.Contains(string(data), `"status":"running"`) {
		t.Fatalf("archive running session %d %s", status, data)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions", map[string]string{"requestId": "excess", "projectPath": home})
	if status != 503 || !strings.Contains(string(data), "codex_limit") {
		t.Fatalf("limit %d %s", status, data)
	}
	if _, err := m.tmux(context.Background(), executable("tmux"), "kill-session", "-t", target(first)); err != nil {
		t.Fatal(err)
	}
	status, data = jsonRequest(t, server, "GET", "/api/host/codex/sessions/"+first.ID, nil)
	if status != 200 || !strings.Contains(string(data), `"status":"lost"`) {
		t.Fatalf("missing runtime %d %s", status, data)
	}
	createSession(t, server, "capacity-recovered", home)
}

func TestRealCodexResizeProtocolFailuresAndAttachmentLimit(t *testing.T) {
	m, server, home := realManager(t)
	session := createSession(t, server, "protocol", home)
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"resize":[111,37]}`)); err != nil {
		t.Fatal(err)
	}
	input(t, conn, "size\r")
	outputUntil(t, conn, "SIZE=37 111")
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"resize":[80,24],"scroll":1}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			break
		}
	}
	eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.connections[session.RunID]) == 0 })
	if _, err := m.tmux(context.Background(), executable("tmux"), "has-session", "-t", target(session)); err != nil {
		t.Fatalf("malformed connection killed persistent task: %v", err)
	}
	connections := []*websocket.Conn{}
	for range 8 {
		connections = append(connections, connectSession(t, server, session))
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + runPath(session) + "/terminal"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	extra, response, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}, "X-AXV-Database-ID": []string{server.databaseID}}})
	if extra != nil {
		_ = extra.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != 503 {
		t.Fatalf("connection capacity: %v %+v", err, response)
	}
	m.Close()
	if _, err := m.tmux(context.Background(), executable("tmux"), "has-session", "-t", target(session)); err != nil {
		t.Fatalf("shutdown killed persistent task: %v", err)
	}
	for _, conn := range connections {
		_ = conn.CloseNow()
	}
}

func TestRealCodexUnexpectedRuntimeErrorPreservesSession(t *testing.T) {
	m, server, home := realManager(t)
	session := createSession(t, server, "runtime-error", home)
	original := m.run
	m.run = func(context.Context, string, ...string) (string, error) {
		return "permission denied", fmt.Errorf("test runtime unavailable")
	}
	status, data := jsonRequest(t, server, "GET", "/api/host/codex/sessions/"+session.ID, nil)
	if status != 200 || !strings.Contains(string(data), "could not be inspected") || !strings.Contains(string(data), `"status":"running"`) {
		t.Fatalf("runtime error not visible: %d %s", status, data)
	}
	stored, err := m.store.CodexSession(context.Background(), session.ID)
	if err != nil || stored.Status != "running" {
		t.Fatalf("runtime error destroyed metadata: %+v %v", stored, err)
	}
	if _, err := m.store.UpdateCodexRun(context.Background(), session.RunID, "stopping", ""); err != nil {
		t.Fatal(err)
	}
	status, data = jsonRequest(t, server, "GET", "/api/host/codex/sessions", nil)
	if status != 200 || !strings.Contains(string(data), session.ID) || !strings.Contains(string(data), `"status":"stopping"`) || !strings.Contains(string(data), "could not be inspected") {
		t.Fatalf("failed stopping session disappeared: %d %s", status, data)
	}
	m.run = original
	status, data = jsonRequest(t, server, "GET", "/api/host/codex/sessions/"+session.ID, nil)
	if status != 200 || !strings.Contains(string(data), `"status":"ended"`) {
		t.Fatalf("runtime recovery: %d %s", status, data)
	}
}

func TestCommandProbeHonorsCallerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := command(ctx, "/bin/sleep", "30"); err == nil {
		t.Fatal("probe ignored deadline")
	}
	if time.Since(start) > time.Second {
		t.Fatal("probe did not return promptly after timeout")
	}
}

func TestRealCodexNaturalExitCleanupCanBeRetried(t *testing.T) {
	m, server, home := realManager(t)
	session := createSession(t, server, "cleanup-retry", home)
	original := m.run
	m.run = func(ctx context.Context, path string, args ...string) (string, error) {
		for _, arg := range args {
			if arg == "kill-session" {
				return "permission denied", fmt.Errorf("test cleanup failure")
			}
		}
		return original(ctx, path, args...)
	}
	if _, err := m.tmux(context.Background(), executable("tmux"), "send-keys", "-t", paneTarget(session), "fail", "Enter"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		status, data := jsonRequest(t, server, "GET", "/api/host/codex/sessions/"+session.ID, nil)
		return status == 200 && strings.Contains(string(data), "Choose End Session to retry")
	})
	stored, err := m.store.CodexSession(context.Background(), session.ID)
	if err != nil || final(stored.Status) {
		t.Fatalf("unretryable final record: %+v %v", stored, err)
	}
	m.run = original
	status, data := jsonRequest(t, server, "POST", runPath(session)+"/end", nil)
	if status != 200 || !strings.Contains(string(data), `"status":"ended"`) {
		t.Fatalf("cleanup retry: %d %s", status, data)
	}
	if _, err := m.tmux(context.Background(), executable("tmux"), "has-session", "-t", target(session)); err == nil {
		t.Fatal("cleanup retry retained dead pane")
	}
}

func TestRealCodexUncertainStartupRetainsRecoverableIdentity(t *testing.T) {
	m, server, home := realManager(t)
	path := filepath.Join(home, "tmux-failing")
	realTmux := executable("tmux")
	script := `#!/bin/sh
creating=0
for arg in "$@"; do
 [ "$arg" = kill-session ] && { printf 'permission denied\n'; exit 1; }
 [ "$arg" = new-session ] && creating=1
done
'` + strings.ReplaceAll(realTmux, "'", "'\\''") + `' "$@"
result=$?
[ "$creating" = 1 ] && exit 1
exit "$result"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m.probe = func(ctx context.Context) Info { info := probe(ctx); info.TmuxPath = path; return info }
	status, data := jsonRequest(t, server, "POST", "/api/host/codex/sessions", map[string]string{"requestId": "uncertain-start", "projectPath": home})
	if status != 503 || !strings.Contains(string(data), "startup_unconfirmed") {
		t.Fatalf("startup ambiguity reported success: %d %s", status, data)
	}
	stored, err := m.store.CodexRequest(context.Background(), "uncertain-start")
	if err != nil || stored.Status != "starting" || !strings.Contains(stored.Reason, "cleanup could not be confirmed") {
		t.Fatalf("startup ambiguity lost identity: %+v %v", stored, err)
	}
	projects, err := m.store.CodexProjects(context.Background())
	if err != nil || len(projects) != 0 {
		t.Fatalf("incomplete startup added a recent project: %+v %v", projects, err)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions", map[string]string{"requestId": "uncertain-start", "projectPath": home})
	if status != 503 || !strings.Contains(string(data), "startup_unconfirmed") {
		t.Fatalf("incomplete setup was reported as running: %d %s", status, data)
	}
	sessions, err := m.store.CodexSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("retry duplicated session: %+v %v", sessions, err)
	}
	status, data = jsonRequest(t, server, "POST", runPath(stored)+"/end", nil)
	if status != 200 || !strings.Contains(string(data), `"status":"ended"`) {
		t.Fatalf("recoverable startup not ended: %d %s", status, data)
	}
}

func TestRealCodexNaturalExitDrainsFinalScreenBeforeEnded(t *testing.T) {
	_, server, home := realManager(t)
	session := createSession(t, server, "final-screen", home)
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	input(t, conn, "exit\r")
	// A delayed consumer must receive the final terminal screen before the
	// lifecycle notification; closing immediately on pane exit can lose it.
	time.Sleep(150 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var output strings.Builder
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ended notification missing: %v; output=%q", err, output.String())
		}
		if kind == websocket.MessageBinary {
			output.Write(data)
			continue
		}
		if strings.Contains(string(data), `"ended":true`) {
			if !strings.Contains(output.String(), "FIXTURE_BYE") {
				t.Fatalf("final screen was lost: %q", output.String())
			}
			break
		}
	}
}

func TestRealCodexSharedServerPreservesUnrelatedStateAndOwnsItsEnvironment(t *testing.T) {
	m, server, home := realManager(t)
	path := executable("tmux")
	if _, err := m.tmux(context.Background(), path, "new-session", "-d", "-s", "unrelated", "/bin/sleep", "60", ";", "set-option", "-t", "unrelated", "destroy-unattached", "off"); err != nil {
		t.Fatal(err)
	}
	// Deliberately configure this owned test server like an existing user server.
	if _, err := m.tmux(context.Background(), path, "set-option", "-g", "base-index", "4", ";", "set-window-option", "-g", "pane-base-index", "3", ";", "set-option", "-g", "destroy-unattached", "on", ";", "set-environment", "-g", "AXV_TEST_OLD", "stale-from-server"); err != nil {
		t.Fatal(err)
	}
	globalEnv, err := m.tmux(context.Background(), path, "show-environment", "-g")
	if err != nil {
		t.Fatal(err)
	}
	globalOptions, err := m.tmux(context.Background(), path, "show-options", "-g")
	if err != nil {
		t.Fatal(err)
	}
	windowOptions, err := m.tmux(context.Background(), path, "show-window-options", "-g")
	if err != nil {
		t.Fatal(err)
	}
	value := "quotes'\" $HOME #{session_name};\nsecond\\line\t世界"
	t.Setenv("AXV_TEST_ENV", value)
	stateFile := filepath.Join(home, "literal-state")
	t.Setenv("AXV_TEST_STATE_FILE", stateFile)
	t.Setenv("AXV_TEST_OLD", "")
	if err := os.Unsetenv("AXV_TEST_OLD"); err != nil {
		t.Fatal(err)
	}
	session := createSession(t, server, "shared", home)
	if !strings.HasPrefix(session.TmuxSession, m.namePrefix+"-") || !filepath.IsAbs(session.TmuxSocket) {
		t.Fatalf("runtime identity not exposed: %+v", session)
	}
	if actual, err := m.sessionTmux(context.Background(), path, session, "show-environment", "-t", target(session), "AXV_TEST_OLD"); err != nil || actual != "-AXV_TEST_OLD" {
		t.Fatal("new session inherited stale shared-server environment")
	}
	if actual, err := m.sessionTmux(context.Background(), path, session, "display-message", "-p", "-t", paneTarget(session), "#{history_limit}"); err != nil || actual != "50000" {
		t.Fatalf("persistent history not configured: %s %v", actual, err)
	}
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	input(t, conn, "state\r")
	outputUntil(t, conn, "STATE_WRITTEN")
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(stateFile); err != nil || string(actual) != canonical+"\x00"+value+"\x00" {
		t.Fatalf("stdin environment transfer changed the child process's literal value: %q (%v)", actual, err)
	}
	_ = conn.CloseNow()
	eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.connections[session.RunID]) == 0 })
	if _, err := m.sessionTmux(context.Background(), path, session, "has-session", "-t", target(session)); err != nil {
		t.Fatalf("inherited destroy-unattached ended Codex: %v", err)
	}
	if status, data := jsonRequest(t, server, "POST", runPath(session)+"/end", nil); status != 200 {
		t.Fatalf("end failed: %d %s", status, data)
	}
	for _, check := range []struct {
		args []string
		want string
	}{{[]string{"show-environment", "-g"}, globalEnv}, {[]string{"show-options", "-g"}, globalOptions}, {[]string{"show-window-options", "-g"}, windowOptions}} {
		if actual, err := m.tmux(context.Background(), path, check.args...); err != nil || actual != check.want {
			t.Fatal("AXV changed shared-server global configuration")
		}
	}
	if _, err := m.tmux(context.Background(), path, "has-session", "-t", "=unrelated"); err != nil {
		t.Fatal("ending AXV stopped an unrelated session")
	}
}

func TestRealCodexDefaultWorkspaceIsLazyAndRuntimeRoutingSurvivesConfigChange(t *testing.T) {
	m, server, _ := realManager(t)
	status, data := jsonRequest(t, server, "GET", "/api/host/codex/info", nil)
	if status != 200 || !strings.Contains(string(data), m.settings.WorkspaceDir) || !strings.Contains(string(data), m.settings.ConfigPath) {
		t.Fatalf("default configuration not exposed: %d %s", status, data)
	}
	if _, err := os.Stat(m.settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("info created workspace: %v", err)
	}
	result := struct{ Session Session }{Session: m.describe(createSession(t, server, "default-workspace", ""))}
	canonical, err := filepath.EvalSymlinks(m.settings.WorkspaceDir)
	if err != nil || result.Session.ProjectPath != canonical || !strings.Contains(result.Session.AttachCommand, "-S ") || !strings.Contains(result.Session.AttachCommand, result.Session.TmuxSession) {
		t.Fatalf("default runtime mismatch: %+v %v", result.Session, err)
	}
	info, err := os.Stat(m.settings.WorkspaceDir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("workspace permissions: %v %v", info, err)
	}
	m.Close()
	server.Close()
	settings := m.settings
	settings.TmuxSocket = "axv-test-unused-new-config"
	restarted, err := New(m.store, settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	server = serveManager(t, restarted)
	conn := connectSession(t, server, result.Session.CodexSession)
	outputUntil(t, conn, "FIXTURE_READY")
	input(t, conn, "saved-routing\r")
	outputUntil(t, conn, "ACK=saved-routing")
	status, data = jsonRequest(t, server, "POST", runPath(result.Session.CodexSession)+"/end", nil)
	if status != 200 || !strings.Contains(string(data), `"status":"ended"`) {
		t.Fatalf("saved routing not used for end: %d %s", status, data)
	}
}

func TestRealCodexConfiguredProxyDoesNotPopulateServerGlobals(t *testing.T) {
	m, server, home := realManager(t)
	for _, key := range proxyKeys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	m.settings.ProxyURL = "http://fixture:credential@127.0.0.1:9191"
	session := createSession(t, server, "configured-proxy", home)
	path := executable("tmux")
	if actual, err := m.sessionTmux(context.Background(), path, session, "show-environment", "-t", target(session), "HTTPS_PROXY"); err != nil || actual != "HTTPS_PROXY="+m.settings.ProxyURL {
		t.Fatal("configured proxy was not applied to the new session")
	}
	if _, err := m.tmux(context.Background(), path, "show-environment", "-g", "HTTPS_PROXY"); err == nil {
		t.Fatal("AXV configured proxy populated shared-server globals")
	}
	status, data := jsonRequest(t, server, "GET", "/api/host/codex/info", nil)
	if status != 200 || !strings.Contains(string(data), `"proxySource":"configuration"`) || !strings.Contains(string(data), `"proxy":"http://127.0.0.1:9191"`) || strings.Contains(string(data), "credential") {
		t.Fatalf("proxy diagnostics not safely reported: %d %s", status, data)
	}
}
