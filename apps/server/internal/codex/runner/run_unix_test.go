//go:build darwin || linux

package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

func TestMain(main *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "app-server":
			os.Exit(fixtureServer())
		case "axv-test-run":
			if Run(os.Args[2]) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "axv-test-descendant":
			signal.Ignore(syscall.SIGTERM)
			_ = os.WriteFile(filepath.Join(os.Getenv("AXV_RUNNER_FIXTURE_DIR"), "descendant.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
			for {
				time.Sleep(time.Hour)
			}
		case "--remote":
			os.Exit(fixtureTUI())
		}
	}
	os.Exit(main.Run())
}

func fixtureServer() int {
	directory := os.Getenv("AXV_RUNNER_FIXTURE_DIR")
	if os.Getenv("AXV_RUNNER_DELAY_CLEANUP") == "1" {
		signal.Ignore(syscall.SIGTERM)
		defer func() {
			_ = os.WriteFile(filepath.Join(directory, "server-cleaning"), nil, 0o600)
			for {
				if _, err := os.Stat(filepath.Join(directory, "allow-server-exit")); err == nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	_ = os.WriteFile(filepath.Join(directory, "server.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	if os.Getenv("AXV_RUNNER_DESCENDANT") == "1" {
		child := exec.Command(os.Args[0], "axv-test-descendant")
		if child.Start() != nil {
			return 3
		}
	}
	log, err := os.OpenFile(filepath.Join(directory, "requests.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 3
	}
	defer log.Close()
	behavior := os.Getenv("AXV_RUNNER_BEHAVIOR")
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		if behavior == "silent" {
			continue
		}
		if behavior == "malformed" {
			fmt.Println("invalid JSON")
			continue
		}
		var message wireMessage
		if json.Unmarshal(input.Bytes(), &message) != nil {
			return 3
		}
		_, _ = log.Write(append(append([]byte(nil), input.Bytes()...), '\n'))
		if len(message.ID) == 0 {
			continue
		}
		response := map[string]any{"id": message.ID, "result": map[string]any{}}
		switch message.Method {
		case "thread/start", "thread/resume", "thread/read":
			if behavior == "missing" {
				response = map[string]any{"id": message.ID, "error": map[string]any{"code": -32600, "message": "history unavailable"}}
			} else {
				id := testThreadID
				if behavior == "wrong" {
					id = otherThreadID
				}
				response = threadResponse(message.ID, id)
			}
		case "thread/name/set":
			if os.WriteFile(filepath.Join(directory, "history-persisted"), []byte(testThreadID), 0o600) != nil {
				return 3
			}
		}
		if writeMessage(os.Stdout, response) != nil {
			return 3
		}
	}
	return 0
}

func fixtureTUI() int {
	directory := os.Getenv("AXV_RUNNER_FIXTURE_DIR")
	_ = os.WriteFile(filepath.Join(directory, "tui.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	_ = os.WriteFile(filepath.Join(directory, "socket-path"), []byte(strings.TrimPrefix(os.Args[2], "unix://")), 0o600)
	_ = os.WriteFile(filepath.Join(directory, "history-home"), []byte(os.Getenv("CODEX_HOME")), 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(os.Args[2], "unix://"))
	}}
	defer transport.CloseIdleConnections()
	client, _, err := websocket.Dial(ctx, "ws://localhost/rpc", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		return 3
	}
	defer client.CloseNow()
	call := func(id int, method string, params any) error {
		data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err := client.Write(ctx, websocket.MessageText, data); err != nil {
			return err
		}
		_, data, err = client.Read(ctx)
		if err != nil {
			return err
		}
		var response wireMessage
		if json.Unmarshal(data, &response) != nil || len(response.Error) != 0 {
			return ErrProtocol
		}
		return nil
	}
	if call(1, "initialize", map[string]any{}) != nil {
		return 3
	}
	method := "thread/start"
	params := map[string]any{}
	if len(os.Args) > 6 && os.Args[6] == "resume" {
		method = "thread/resume"
		params["threadId"] = os.Args[7]
	}
	if call(2, method, params) != nil {
		return 3
	}
	data, err := os.ReadFile(filepath.Join(directory, "state.json"))
	var state State
	if err != nil || json.Unmarshal(data, &state) != nil || state.Status != StatusReady || state.ThreadID != testThreadID {
		return 4
	}
	fmt.Println("READY")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) == "crash" {
		return 7
	}
	return 0
}

func runnerFixture(t *testing.T) (string, string, []string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "AXV_RUNNER_FIXTURE_DIR="+directory)
	return directory, executable, environment
}

func TestVerifyHistoryUsesOnlyExactMetadataAndPrivateHome(t *testing.T) {
	directory, executable, environment := runnerFixture(t)
	environment = append(environment, "CODEX_HOME=/unrelated/home")
	if err := VerifyHistory(context.Background(), executable, directory, directory, environment, testThreadID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected requests: %s", data)
	}
	for index, expected := range []string{"initialize", "initialized", "thread/read"} {
		var message wireMessage
		if json.Unmarshal([]byte(lines[index]), &message) != nil || message.Method != expected {
			t.Fatalf("unexpected request %s", lines[index])
		}
		if expected == "thread/read" {
			var params struct {
				ThreadID     string `json:"threadId"`
				IncludeTurns bool   `json:"includeTurns"`
			}
			if json.Unmarshal(message.Params, &params) != nil || params.ThreadID != testThreadID || params.IncludeTurns {
				t.Fatalf("history verification read other data: %s", message.Params)
			}
		}
	}
	assertStopped(t, fixturePID(t, directory, "server"))
}

func TestVerifyHistoryDistinguishesMissingAndProtocolErrors(t *testing.T) {
	for _, item := range []struct {
		behavior string
		expected error
	}{
		{"missing", ErrHistoryUnavailable},
		{"wrong", ErrProtocol},
		{"malformed", ErrProtocol},
	} {
		t.Run(item.behavior, func(t *testing.T) {
			directory, executable, environment := runnerFixture(t)
			environment = append(environment, "AXV_RUNNER_BEHAVIOR="+item.behavior)
			// These assertions classify responses, not process startup speed. The
			// production helper retains its own deadline for an unresponsive child.
			if err := VerifyHistory(context.Background(), executable, directory, directory, environment, testThreadID); !errors.Is(err, item.expected) {
				t.Fatalf("error = %v, want %v", err, item.expected)
			}
			assertStopped(t, fixturePID(t, directory, "server"))
		})
	}
}

func TestVerifyHistoryCancelsASilentChildAfterStartup(t *testing.T) {
	directory, executable, environment := runnerFixture(t)
	environment = append(environment, "AXV_RUNNER_BEHAVIOR=silent")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- VerifyHistory(ctx, executable, directory, directory, environment, testThreadID)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("history helper did not drain after cancellation")
		}
	})
	pid := fixturePID(t, directory, "server")
	// Begin the response budget after the child has started. Compiler and race
	// runtime scheduling must not consume the fixture's protocol timeout.
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want %v", err, ErrUnavailable)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("history helper ignored caller cancellation")
	}
	assertStopped(t, pid)
}

func TestVerifyHistoryHasAnInternalDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the actual bounded helper deadline")
	}
	directory, executable, environment := runnerFixture(t)
	environment = append(environment, "AXV_RUNNER_BEHAVIOR=silent")
	started := time.Now()
	if err := VerifyHistory(context.Background(), executable, directory, directory, environment, testThreadID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 14*time.Second || elapsed > 20*time.Second {
		t.Fatalf("verification deadline elapsed %s", elapsed)
	}
	assertStopped(t, fixturePID(t, directory, "server"))
}

func fixturePID(t *testing.T, directory, name string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(directory, name+".pid"))
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not record a PID", name)
	return 0
}

func assertStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		// A killed orphan can briefly remain a zombie until init reaps it.
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil && strings.Contains(string(data), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("owned child %d is still running", pid)
}

func TestRunOwnsTUIAndServerDescendants(t *testing.T) {
	for _, ending := range []string{"quit", "crash", "hangup"} {
		t.Run(ending, func(t *testing.T) {
			directory, executable, environment := runnerFixture(t)
			socketRoot := filepath.Join(shortSocketHome(t), "run", "codex")
			manifest := Manifest{SessionID: "session", RunID: "run", ProjectPath: directory, CodexPath: executable, HistoryHome: directory, SocketRoot: socketRoot, OwnershipPath: filepath.Join(directory, "ownership", "conversation.lock"), Mode: ModeNew}
			data, _ := json.Marshal(manifest)
			manifestPath := filepath.Join(directory, "manifest.json")
			if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "axv-test-run", manifestPath)
			command.Env = append(environment, "AXV_RUNNER_DESCENDANT=1", "CODEX_HOME=/unrelated/provider-home", "AXV_HOME=/unrelated/profile")
			configureServerProcess(command)
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			process, err := startProcess(command, stdin, stdout, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(process.stop)
			ready := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- strings.TrimSpace(line) }()
			select {
			case line := <-ready:
				if line != "READY" {
					t.Fatalf("TUI failed before ready: %q", line)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("TUI startup timed out")
			}
			serverPID := fixturePID(t, directory, "server")
			tuiPID := fixturePID(t, directory, "tui")
			descendantPID := fixturePID(t, directory, "descendant")
			socket, err := os.ReadFile(filepath.Join(directory, "socket-path"))
			if err != nil || filepath.Dir(filepath.Dir(string(socket))) != socketRoot {
				t.Fatalf("runner socket escaped manifest root: %q, %v", socket, err)
			}
			if info, err := os.Stat(string(socket)); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
				t.Fatalf("runner socket is not private: %v, %v", info, err)
			}
			history, err := os.ReadFile(filepath.Join(directory, "history-home"))
			if err != nil || string(history) != manifest.HistoryHome {
				t.Fatalf("provider home changed: %q, %v", history, err)
			}
			// No frontend is connected to this runner. Its ready conversation
			// and owned processes remain alive until a terminal exit or signal.
			if syscall.Kill(serverPID, 0) != nil || syscall.Kill(tuiPID, 0) != nil {
				t.Fatal("detached runtime did not stay alive")
			}
			if proof, err := ReadExit(directory, manifest.RunID); proof != nil || err != nil {
				t.Fatalf("live runner published exit proof: %+v, %v", proof, err)
			}
			if ending == "hangup" {
				_ = command.Process.Signal(syscall.SIGHUP)
			} else {
				_, _ = fmt.Fprintln(stdin, ending)
			}
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				t.Fatal("runner did not exit")
			}
			assertStopped(t, serverPID)
			assertStopped(t, tuiPID)
			assertStopped(t, descendantPID)
			if _, err := os.Stat(filepath.Dir(string(socket))); !os.IsNotExist(err) {
				t.Fatalf("runner socket directory survived exit: %v", err)
			}
			state := stateAt(t, filepath.Join(directory, "state.json"))
			if state.ThreadID != testThreadID {
				t.Fatalf("exit lost confirmed conversation: %+v", state)
			}
			if ending == "crash" && (state.Status != StatusFailed || state.ErrorCode != "codex_runtime_failed" || process.err == nil) {
				t.Fatalf("nonzero TUI exit not recorded: state=%+v error=%v", state, process.err)
			}
			// A race-instrumented fixture can exceed the existing TUI-close
			// grace period. The proof must match the wrapper's actual outcome.
			code := command.ProcessState.ExitCode()
			proof, err := ReadExit(directory, manifest.RunID)
			if err != nil || proof == nil || proof.ExitCode != code {
				t.Fatalf("finished runner proof = %+v, %v; want code %d", proof, err, code)
			}
		})
	}
}

func TestRunKeepsConversationOwnershipUntilServerStops(t *testing.T) {
	firstDir, executable, environment := runnerFixture(t)
	secondDir, _, secondEnvironment := runnerFixture(t)
	ownershipPath := filepath.Join(firstDir, "ownership", "conversation.lock")
	start := func(directory string, environment []string, runID, mode string) (*ownedProcess, io.WriteCloser, <-chan string) {
		t.Helper()
		manifest := Manifest{SessionID: "session", RunID: runID, ProjectPath: directory, CodexPath: executable, HistoryHome: firstDir, SocketRoot: filepath.Join(shortSocketHome(t), "run", "codex"), OwnershipPath: ownershipPath, Mode: mode}
		if mode == ModeResume {
			manifest.ThreadID = testThreadID
		}
		data, _ := json.Marshal(manifest)
		manifestPath := filepath.Join(directory, "manifest.json")
		if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "axv-test-run", manifestPath)
		command.Env = environment
		configureServerProcess(command)
		stdin, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		process, err := startProcess(command, stdin, stdout, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(process.stop)
		ready := make(chan string, 1)
		go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- strings.TrimSpace(line) }()
		return process, stdin, ready
	}
	waitFile := func(path string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("fixture did not publish %s", path)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitReady := func(ready <-chan string) {
		t.Helper()
		select {
		case line := <-ready:
			if line != "READY" {
				t.Fatalf("runner did not become ready: %q", line)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runner readiness timed out")
		}
	}
	first, _, ready := start(firstDir, append(environment, "AXV_RUNNER_DELAY_CLEANUP=1"), "first", ModeNew)
	waitReady(ready)
	serverPID := fixturePID(t, firstDir, "server")
	if err := first.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	waitFile(filepath.Join(firstDir, "server-cleaning"))
	// A second terminal hangup during cleanup must not restore the default
	// signal handler and abandon the app-server or release ownership early.
	if err := first.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	lock, err := filelock.TryLock(ownershipPath)
	if lock != nil {
		lock.Close()
	}
	if !contextLockContended(err) {
		t.Fatalf("ownership released before provider exit: %v", err)
	}
	if proof, err := ReadExit(firstDir, "first"); proof != nil || err != nil {
		t.Fatalf("exit proof published before provider cleanup: %+v, %v", proof, err)
	}
	second, input, resumed := start(secondDir, secondEnvironment, "second", ModeResume)
	waitFile(filepath.Join(secondDir, "state.json"))
	if _, err := os.Stat(filepath.Join(secondDir, "server.pid")); !os.IsNotExist(err) {
		t.Fatalf("new provider started before predecessor cleanup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstDir, "allow-server-exit"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitReady(resumed)
	select {
	case <-first.done:
	case <-time.After(3 * time.Second):
		t.Fatal("predecessor runner did not finish after its provider")
	}
	if proof, err := ReadExit(firstDir, "first"); err != nil || proof == nil || proof.ExitCode != 0 {
		t.Fatalf("cleaned predecessor has no exit proof: %+v, %v", proof, err)
	}
	assertStopped(t, serverPID)
	_, _ = fmt.Fprintln(input, "quit")
	select {
	case <-second.done:
	case <-time.After(3 * time.Second):
		t.Fatal("resumed runner did not stop")
	}
}
