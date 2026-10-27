// Package terminal owns ephemeral host shell sessions.
package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

const maxSessions = 4

// Manager tracks upgraded connections separately from http.Server, whose Shutdown
// does not close hijacked WebSockets. Close waits until every shell has been reaped.
type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	wg        sync.WaitGroup
	active    int
	closed    bool
	resolve   func() (shellConfig, error)
	directory func(string) (string, error)
	start     func(shellConfig, int, int) (process, error)
}

// New enforces local access or an explicitly authenticated remote transport.
func New() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{ctx: ctx, cancel: cancel, resolve: resolveShell, directory: projectDirectory, start: startProcess}
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) acquire() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active >= maxSessions {
		return false
	}
	m.active++
	m.wg.Add(1)
	return true
}

func (m *Manager) release() {
	m.mu.Lock()
	m.active--
	m.mu.Unlock()
	m.wg.Done()
}

// Info reports availability without creating a shell or allocating a session.
func (m *Manager) Info(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r, false) {
		jsonResponse(w, http.StatusForbidden, map[string]any{"supported": false, "reason": "Terminal is available only from the same local origin.", "error": "Terminal is available only from the same local origin.", "code": "terminal_forbidden"})
		return
	}
	config, err := m.resolve()
	if err != nil {
		jsonResponse(w, http.StatusOK, map[string]any{"supported": false, "reason": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"supported": true, "shell": config.path})
}

// Connect validates access and protocol parameters before upgrading or spawning.
func (m *Manager) Connect(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r, true) {
		jsonResponse(w, http.StatusForbidden, map[string]string{"error": "Terminal requires a same-origin connection from this computer.", "code": "terminal_forbidden"})
		return
	}
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	cols, rows, ok := dimensions(query)
	if queryErr != nil || !ok {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "Use cols 2–500 and rows 1–200, with at most one optional projectPath parameter.", "code": "invalid_terminal_size"})
		return
	}
	if !m.acquire() {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "Terminal is stopping or all four sessions are in use. Close another Terminal window and try again.", "code": "terminal_limit"})
		return
	}
	defer m.release()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	stopRequest := context.AfterFunc(r.Context(), cancel)
	defer stopRequest()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		<-ctx.Done()
		_ = conn.CloseNow()
	}()
	defer func() { cancel(); <-watchDone }()
	if ctx.Err() != nil {
		return
	}
	config, err := m.resolve()
	if err != nil {
		writeControl(ctx, conn, map[string]string{"error": err.Error()})
		closeSocket(conn, websocket.StatusInternalError, "Shell unavailable")
		return
	}
	if project, provided := query["projectPath"]; provided {
		config.dir, err = m.directory(project[0])
		if err != nil {
			writeControl(ctx, conn, map[string]string{"error": err.Error(), "code": "terminal_project_unavailable"})
			closeSocket(conn, websocket.StatusPolicyViolation, "Project directory unavailable")
			return
		}
	}
	shell, err := m.start(config, cols, rows)
	if err != nil {
		writeControl(ctx, conn, map[string]string{"error": "Could not start the host shell: " + err.Error()})
		closeSocket(conn, websocket.StatusInternalError, "Shell failed to start")
		return
	}
	runSession(ctx, conn, shell)
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func localRequest(r *http.Request, requireOrigin bool) bool {
	return httpaccess.AllowRequest(r, requireOrigin)
}

func validSize(cols, rows int) bool { return cols >= 2 && cols <= 500 && rows >= 1 && rows <= 200 }

func dimensions(query url.Values) (int, int, bool) {
	cols, rows := 80, 24
	for key, values := range query {
		if (key != "cols" && key != "rows" && key != "projectPath") || len(values) != 1 {
			return 0, 0, false
		}
		if key == "projectPath" {
			continue
		}
		n, err := strconv.Atoi(values[0])
		if err != nil {
			return 0, 0, false
		}
		if key == "cols" {
			cols = n
		} else {
			rows = n
		}
	}
	return cols, rows, validSize(cols, rows)
}
