//go:build darwin || linux

package runner

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// Run owns both the official TUI and its private stdio app-server. It is
// launched inside the tmux pane, independently of the HTTP server's lifetime.
func Run(manifestPath string) error {
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	statePath := filepath.Join(filepath.Dir(manifestPath), "state.json")
	starting := State{RunID: manifest.RunID, ThreadID: manifest.ThreadID, HistoryHome: manifest.HistoryHome, Status: StatusStarting}
	if writeState(statePath, starting) != nil {
		return ErrUnavailable
	}
	fail := func(code string) error {
		writeFailure(statePath, starting, code)
		return ErrUnavailable
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM)
	defer cancel()
	// While the TUI normally consumes Ctrl-C as raw terminal input, a signal
	// during startup must not make its wrapper abandon owned child processes.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	socketPath, cleanupSocket, err := createSocketPath(manifest.SocketRoot)
	if err != nil {
		return fail("codex_start_failed")
	}
	defer cleanupSocket()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fail("codex_start_failed")
	}
	defer listener.Close()
	if os.Chmod(socketPath, 0o600) != nil {
		return fail("codex_start_failed")
	}
	server, err := startServer(manifest.CodexPath, manifest.ProjectPath, manifest.HistoryHome, os.Environ())
	if err != nil {
		return fail("codex_start_failed")
	}
	defer server.stop()
	var accepted atomic.Bool
	bridgeDone := make(chan error, 1)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rpc" || accepted.Swap(true) {
			http.Error(response, "This terminal already has a connection.", http.StatusConflict)
			return
		}
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			bridgeDone <- ErrUnavailable
			return
		}
		connection.SetReadLimit(maxMessageBytes)
		bridge := newBridge(manifest, statePath, server.stdin, connection)
		bridgeDone <- bridge.run(ctx, server.stdout)
	})
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	defer httpServer.Close()
	go func() { _ = httpServer.Serve(listener) }()
	arguments := []string{"--remote", "unix://" + socketPath, "--no-alt-screen", "--cd", manifest.ProjectPath}
	if manifest.Mode == ModeResume {
		arguments = append(arguments, "resume", manifest.ThreadID)
	}
	command := exec.Command(manifest.CodexPath, arguments...)
	command.Dir = manifest.ProjectPath
	command.Env = historyEnvironment(os.Environ(), manifest.HistoryHome)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	tui, err := startProcess(command, nil, nil, false)
	if err != nil {
		return fail("codex_start_failed")
	}
	defer tui.stop()
	defer cancel()
	select {
	case <-ctx.Done():
		return nil
	case <-tui.done:
		if tui.err != nil && ctx.Err() == nil {
			return fail("codex_runtime_failed")
		}
		return nil
	case err := <-bridgeDone:
		if err != nil {
			return err
		}
		// A TUI closes its socket just before exiting. Wait for the child so
		// that a nonzero exit cannot race a clean WebSocket EOF and disappear.
		select {
		case <-ctx.Done():
			return nil
		case <-tui.done:
			if tui.err != nil {
				return fail("codex_runtime_failed")
			}
			return nil
		case <-time.After(500 * time.Millisecond):
			return fail("codex_runtime_failed")
		}
	case <-server.done:
		// The bridge records failures after initialization, including a
		// previously confirmed identity. Give it the EOF before stopping it.
		select {
		case err := <-bridgeDone:
			return err
		case <-time.After(100 * time.Millisecond):
			return fail("codex_runtime_failed")
		}
	}
}
