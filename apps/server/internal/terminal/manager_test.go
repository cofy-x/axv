package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func testServer(t *testing.T, manager *Manager) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/terminal", manager.Connect)
	mux.HandleFunc("GET /api/terminal/info", manager.Info)
	server := httptest.NewServer(mux)
	t.Cleanup(func() { manager.Close(); server.Close() })
	return server
}

func dialTerminal(t *testing.T, server *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal"+query,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func readMessage(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return kind, data
}

func awaitReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		kind, data := readMessage(t, conn)
		if kind == websocket.MessageText {
			var ready struct {
				Ready bool `json:"ready"`
			}
			if json.Unmarshal(data, &ready) != nil || !ready.Ready {
				t.Fatalf("expected ready, got %s", data)
			}
			return
		}
	}
}

func sendMessage(t *testing.T, conn *websocket.Conn, kind websocket.MessageType, data string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, kind, []byte(data)); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not complete within five seconds")
}

func TestAccessRejectsBeforeShellCreation(t *testing.T) {
	manager := New()
	defer manager.Close()
	var starts atomic.Int32
	manager.resolve = func() (shellConfig, error) {
		t.Fatal("must not resolve shell before access and upgrade")
		return shellConfig{}, nil
	}
	manager.directory = func(string) (string, error) {
		t.Fatal("must not access project directories before local access and upgrade")
		return "", nil
	}
	manager.start = func(shellConfig, int, int) (process, error) { starts.Add(1); return nil, errors.New("unexpected") }
	cases := []struct {
		name, peer, host, origin, query string
		status                          int
	}{
		{"remote peer", "192.0.2.1:1234", "localhost:3001", "http://localhost:3001", "", 403},
		{"remote project", "192.0.2.1:1234", "localhost:3001", "http://localhost:3001", "?projectPath=/private/project", 403},
		{"rebinding", "127.0.0.1:1234", "attacker.example:3001", "http://attacker.example:3001", "", 403},
		{"wildcard host", "127.0.0.1:1234", "0.0.0.0:3001", "http://0.0.0.0:3001", "", 403},
		{"missing origin", "127.0.0.1:1234", "localhost:3001", "", "", 403},
		{"null origin", "127.0.0.1:1234", "localhost:3001", "null", "", 403},
		{"cross origin", "127.0.0.1:1234", "localhost:3001", "http://localhost:5173", "", 403},
		{"cross scheme", "127.0.0.1:1234", "localhost:3001", "https://localhost:3001", "", 403},
		{"origin path", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001/path", "", 403},
		{"origin credentials", "127.0.0.1:1234", "localhost:3001", "http://user@localhost:3001", "", 403},
		{"origin query", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001?", "", 403},
		{"origin fragment", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001#", "", 403},
		{"many origins", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001 http://evil.test", "", 403},
		{"cols overflow", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?cols=65537", 400},
		{"rows invalid", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?rows=-1", 400},
		{"duplicate dimension", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?cols=80&cols=90", 400},
		{"duplicate project", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?projectPath=/first&projectPath=/second", 400},
		{"command query", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?command=whoami", 400},
		{"bad query encoding", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?cols=%xx", 400},
		{"no upgrade", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "", 426},
		{"project without upgrade", "127.0.0.1:1234", "localhost:3001", "http://localhost:3001", "?projectPath=/project", 426},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/terminal"+tc.query, nil)
			r.RemoteAddr, r.Host = tc.peer, tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:3001")
			w := httptest.NewRecorder()
			manager.Connect(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body)
			}
			if tc.status != 426 && !json.Valid(w.Body.Bytes()) {
				t.Fatalf("non-JSON rejection: %s", w.Body)
			}
		})
	}
	if starts.Load() != 0 {
		t.Fatal("rejected request created a shell")
	}
}

func TestAuthenticatedAccessDoesNotRewriteTransport(t *testing.T) {
	const peer = "100.101.102.103:45678"
	const origin = "https://workstation.example.ts.net"
	manager := New()
	defer manager.Close()
	manager.resolve = func() (shellConfig, error) { return shellConfig{path: "/test/shell"}, nil }
	r := httptest.NewRequest(http.MethodGet, origin+"/api/terminal/info", nil)
	r.RemoteAddr = peer
	w := httptest.NewRecorder()
	manager.Info(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated peer bypassed access protection: %d", w.Code)
	}
	authenticated, err := httpaccess.WithAuthenticatedRemote(r, origin)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	manager.Info(w, authenticated)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated peer rejected for info: %d %s", w.Code, w.Body)
	}
	manager.resolve = func() (shellConfig, error) {
		t.Fatal("shell resolved before WebSocket upgrade")
		return shellConfig{}, nil
	}
	w = httptest.NewRecorder()
	manager.Connect(w, authenticated)
	if w.Code != http.StatusForbidden {
		t.Fatalf("upgrade without Origin bypassed protection: %d", w.Code)
	}
	authenticated.Header.Set("Origin", origin)
	w = httptest.NewRecorder()
	manager.Connect(w, authenticated)
	if w.Code != http.StatusUpgradeRequired || authenticated.RemoteAddr != peer || authenticated.Host != "workstation.example.ts.net" {
		t.Fatalf("authenticated upgrade changed transport or guard: %d %s", w.Code, w.Body)
	}
}

func TestRequestCancellationClosesUpgradedShell(t *testing.T) {
	manager, started := fakeManager()
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manager.Connect(w, r.WithContext(requestContext))
	}))
	t.Cleanup(func() { manager.Close(); server.Close() })
	conn := dialTerminal(t, server, "")
	awaitReady(t, conn)
	shell := <-started
	cancelRequest()
	select {
	case <-shell.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled authenticated request left its shell alive")
	}
}

func TestLocalOriginAndInfo(t *testing.T) {
	for _, host := range []string{"localhost:5173", "127.0.0.1:3001", "[::1]:3001"} {
		r := httptest.NewRequest("GET", "/api/terminal", nil)
		r.Host, r.RemoteAddr = host, "[::1]:1234"
		r.Header.Set("Origin", "http://"+host)
		if !localRequest(r, true) {
			t.Errorf("local origin rejected: %s", host)
		}
	}
	manager := New()
	defer manager.Close()
	manager.resolve = func() (shellConfig, error) { return shellConfig{path: "/bin/test-shell"}, nil }
	r := httptest.NewRequest("GET", "/api/terminal/info", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:1234"
	w := httptest.NewRecorder()
	manager.Info(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"supported":true`) {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	manager.resolve = func() (shellConfig, error) { return shellConfig{}, errors.New("Unsupported test platform") }
	w = httptest.NewRecorder()
	manager.Info(w, r)
	if !strings.Contains(w.Body.String(), `"supported":false`) || !strings.Contains(w.Body.String(), "Unsupported test platform") {
		t.Fatal(w.Body.String())
	}
}

func TestProjectDirectoryOverridesOnlyTheWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project's 中文")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	m.resolve = func() (shellConfig, error) {
		return shellConfig{path: "/test/shell", dir: home, env: []string{"HOME=" + home}}, nil
	}
	started := make(chan shellConfig, 1)
	m.start = func(config shellConfig, _, _ int) (process, error) {
		started <- config
		return newBlockedProcess(), nil
	}
	server := testServer(t, m)
	cases := []struct{ query, want string }{{"", home}, {"?projectPath=" + url.QueryEscape(project), canonical}}
	alias := filepath.Join(home, "linked-project")
	if err := os.Symlink(project, alias); err == nil {
		cases = append(cases, struct{ query, want string }{"?projectPath=" + url.QueryEscape(alias), canonical})
	}
	for _, tc := range cases {
		conn := dialTerminal(t, server, tc.query)
		awaitReady(t, conn)
		config := <-started
		if config.dir != tc.want || config.path != "/test/shell" || len(config.env) != 1 || config.env[0] != "HOME="+home {
			t.Fatalf("working directory changed unrelated shell configuration: %#v", config)
		}
		_ = conn.CloseNow()
		waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 0 })
	}
}

func TestInvalidProjectDirectoryNeverStartsAFallbackShell(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.resolve = func() (shellConfig, error) { return shellConfig{dir: home}, nil }
	var starts atomic.Int32
	m.start = func(shellConfig, int, int) (process, error) {
		starts.Add(1)
		return nil, errors.New("must not start")
	}
	server := testServer(t, m)
	for _, tc := range []struct{ name, path, message string }{
		{"empty", "", "absolute host project directory"},
		{"relative", "relative/project", "absolute host project directory"},
		{"oversized", "/" + strings.Repeat("x", 4096), "at most 4096 bytes"},
		{"nul", home + "\x00", "absolute host project directory"},
		{"invalid UTF-8", home + string([]byte{0xff}), "absolute host project directory"},
		{"missing", filepath.Join(home, "missing"), "does not exist or cannot be accessed"},
		{"file", file, "file, not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := dialTerminal(t, server, "?projectPath="+url.QueryEscape(tc.path))
			kind, data := readMessage(t, conn)
			var control struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if kind != websocket.MessageText || json.Unmarshal(data, &control) != nil || control.Code != "terminal_project_unavailable" || !strings.Contains(control.Error, tc.message) {
				t.Fatalf("project error not visible: %s", data)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("invalid project did not close its connection: %v", err)
			}
			waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 0 })
		})
	}
	if starts.Load() != 0 {
		t.Fatal("invalid project started a fallback shell")
	}
}

type blockedProcess struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	done    chan struct{}
	writing chan struct{}
	once    sync.Once
}

func newBlockedProcess() *blockedProcess {
	r, w := io.Pipe()
	return &blockedProcess{reader: r, writer: w, done: make(chan struct{}), writing: make(chan struct{}, 1)}
}

func (p *blockedProcess) Read(data []byte) (int, error) { return p.reader.Read(data) }

func (p *blockedProcess) Write([]byte) (int, error) {
	select {
	case p.writing <- struct{}{}:
	default:
	}
	<-p.done
	return 0, io.ErrClosedPipe
}

func (p *blockedProcess) Resize(int, int) error { return nil }

func (p *blockedProcess) Done() <-chan struct{} { return p.done }

func (p *blockedProcess) Close() {
	p.once.Do(func() { close(p.done); _ = p.writer.Close(); _ = p.reader.Close() })
}

func fakeManager() (*Manager, chan *blockedProcess) {
	m := New()
	started := make(chan *blockedProcess, maxSessions)
	m.resolve = func() (shellConfig, error) { return shellConfig{}, nil }
	m.start = func(shellConfig, int, int) (process, error) { p := newBlockedProcess(); started <- p; return p, nil }
	return m, started
}

func TestShutdownUnblocksInputAndEnforcesSessionLimit(t *testing.T) {
	m, started := fakeManager()
	server := testServer(t, m)
	for range maxSessions {
		conn := dialTerminal(t, server, "")
		awaitReady(t, conn)
		p := <-started
		sendMessage(t, conn, websocket.MessageBinary, "blocked input")
		select {
		case <-p.writing:
		case <-time.After(time.Second):
			t.Fatal("input was not forwarded")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}}})
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("fifth session accepted")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("limit response: %v %v", response, err)
	}
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind shell input")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != 0 {
		t.Fatalf("%d sessions survived shutdown", m.active)
	}
}

func TestStartupAndControlErrorsAreVisible(t *testing.T) {
	t.Run("startup", func(t *testing.T) {
		m := New()
		m.resolve = func() (shellConfig, error) { return shellConfig{}, nil }
		m.start = func(shellConfig, int, int) (process, error) { return nil, errors.New("test launch failure") }
		conn := dialTerminal(t, testServer(t, m), "")
		kind, data := readMessage(t, conn)
		if kind != websocket.MessageText || !strings.Contains(string(data), "test launch failure") {
			t.Fatalf("error not visible: %s", data)
		}
	})
	for _, control := range []string{`{"resize":[0,24]}`, `{"resize":[80,24,1]}`, `{"resize":[80,24],"command":"sh"}`, `{"resize":[80,24]}{}`, `null`} {
		t.Run(control, func(t *testing.T) {
			m, started := fakeManager()
			conn := dialTerminal(t, testServer(t, m), "")
			awaitReady(t, conn)
			p := <-started
			sendMessage(t, conn, websocket.MessageText, control)
			kind, data := readMessage(t, conn)
			if kind != websocket.MessageText || !strings.Contains(string(data), `"error"`) {
				t.Fatalf("error not visible: %s", data)
			}
			select {
			case <-p.done:
			case <-time.After(time.Second):
				t.Fatal("bad control kept shell alive")
			}
		})
	}
}

func TestOversizedInputClosesShell(t *testing.T) {
	m, started := fakeManager()
	conn := dialTerminal(t, testServer(t, m), "")
	awaitReady(t, conn)
	p := <-started
	sendMessage(t, conn, websocket.MessageBinary, strings.Repeat("x", maxMessageBytes+1))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversize close: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("oversized input kept shell alive")
	}
}

func TestDisconnectUnblocksShellInput(t *testing.T) {
	m, started := fakeManager()
	conn := dialTerminal(t, testServer(t, m), "")
	awaitReady(t, conn)
	p := <-started
	sendMessage(t, conn, websocket.MessageBinary, "blocked input")
	select {
	case <-p.writing:
	case <-time.After(time.Second):
		t.Fatal("input was not forwarded")
	}
	_ = conn.CloseNow()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("disconnect could not interrupt blocked shell input")
	}
}

func TestInputBackpressureIsBounded(t *testing.T) {
	m, started := fakeManager()
	conn := dialTerminal(t, testServer(t, m), "")
	awaitReady(t, conn)
	p := <-started
	sendMessage(t, conn, websocket.MessageBinary, "blocked input")
	<-p.writing
	for range 9 {
		sendMessage(t, conn, websocket.MessageBinary, "queued input")
	}
	_, data := readMessage(t, conn)
	if !strings.Contains(string(data), "faster than the shell") {
		t.Fatalf("backpressure error missing: %s", data)
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("backpressure left shell running")
	}
}

type recoveringProcess struct {
	*blockedProcess
	resume   chan struct{}
	accepted chan string
}

func (p *recoveringProcess) Write(data []byte) (int, error) {
	select {
	case p.writing <- struct{}{}:
	default:
	}
	select {
	case <-p.done:
		return 0, io.ErrClosedPipe
	case <-p.resume:
		p.accepted <- string(data)
		return len(data), nil
	}
}

func TestHealthyInputBurstSurvivesBriefShellStall(t *testing.T) {
	p := &recoveringProcess{blockedProcess: newBlockedProcess(), resume: make(chan struct{}), accepted: make(chan string, 64)}
	m := New()
	m.resolve = func() (shellConfig, error) { return shellConfig{}, nil }
	m.start = func(shellConfig, int, int) (process, error) { return p, nil }
	conn := dialTerminal(t, testServer(t, m), "")
	awaitReady(t, conn)
	const burst = "0123456789abcdefghijklmnopqrstuv"
	sendMessage(t, conn, websocket.MessageBinary, burst[:1])
	<-p.writing
	for _, character := range burst[1:] {
		sendMessage(t, conn, websocket.MessageBinary, string(character))
	}
	// Simulate a brief scheduler/PTY stall while a healthy browser types rapidly.
	time.Sleep(100 * time.Millisecond)
	close(p.resume)
	var received strings.Builder
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for received.Len() < len(burst) {
		select {
		case data := <-p.accepted:
			received.WriteString(data)
		case <-p.done:
			t.Fatalf("healthy burst disconnected after receiving %q", received.String())
		case <-deadline.C:
			t.Fatalf("healthy burst stalled after receiving %q", received.String())
		}
	}
	if received.String() != burst {
		t.Fatalf("burst input reordered or lost: %q", received.String())
	}
	select {
	case <-p.done:
		t.Fatal("healthy shell closed after burst")
	default:
	}
}

func TestSaturatedInputStillAllowsTeardown(t *testing.T) {
	for _, action := range []string{"disconnect", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			m, started := fakeManager()
			conn := dialTerminal(t, testServer(t, m), "")
			awaitReady(t, conn)
			p := <-started
			sendMessage(t, conn, websocket.MessageBinary, "blocked input")
			<-p.writing
			for range 9 {
				sendMessage(t, conn, websocket.MessageBinary, "queued input")
			}
			time.Sleep(50 * time.Millisecond)
			if action == "disconnect" {
				_ = conn.CloseNow()
			} else {
				m.Close()
			}
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				t.Fatal("saturated queue prevented session teardown")
			}
		})
	}
}
