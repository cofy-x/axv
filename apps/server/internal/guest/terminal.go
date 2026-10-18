package guest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type terminals struct {
	service  *service
	mu       sync.Mutex
	sessions map[string]*terminal
	requests map[string]string
	closed   bool
}

type terminal struct {
	mu            sync.Mutex
	terminateOnce sync.Once
	id, request   string
	file          *os.File
	cmd           *exec.Cmd
	done          chan struct{}
	ring          []byte
	truncated     bool
	ended         bool
	attached      bool
	output        chan []byte
}

func newTerminals(s *service) *terminals {
	return &terminals{service: s, sessions: map[string]*terminal{}, requests: map[string]string{}}
}

func (t *terminals) lookup(id string) *terminal {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[id]
}

func (t *terminals) create(w http.ResponseWriter, r *http.Request) {
	var req protocol.TerminalRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, "invalid_request", err)
		return
	}
	if len(req.RequestID) < 8 || len(req.RequestID) > 128 {
		fail(w, 400, "invalid_request", fmt.Errorf("requestId must contain 8–128 bytes"))
		return
	}
	if req.Cols == 0 {
		req.Cols = 80
	}
	if req.Rows == 0 {
		req.Rows = 24
	}
	if req.Cols < 2 || req.Cols > 500 || req.Rows < 1 || req.Rows > 200 {
		fail(w, 400, "invalid_request", fmt.Errorf("invalid terminal size"))
		return
	}
	raw, _ := json.Marshal(req)
	t.mu.Lock()
	defer t.mu.Unlock()
	if prior, ok := t.requests[req.RequestID]; ok {
		session := t.sessions[prior]
		if session.request != string(raw) {
			fail(w, 409, "idempotency_conflict", fmt.Errorf("requestId was used for a different terminal request"))
			return
		}
		respond(w, 200, session.description(t.service.bootID))
		return
	}
	if t.closed || len(t.sessions) >= 1024 {
		fail(w, 503, "terminal_limit", fmt.Errorf("guest terminal history is full or the service is stopping"))
		return
	}
	active := 0
	for _, session := range t.sessions {
		session.mu.Lock()
		if !session.ended {
			active++
		}
		session.mu.Unlock()
	}
	if active >= 16 {
		fail(w, 503, "terminal_limit", fmt.Errorf("end another guest terminal before starting a new one"))
		return
	}
	dir, err := t.service.directory(req.Directory)
	if err != nil {
		fail(w, 400, "invalid_directory", err)
		return
	}
	f, cmd, err := startTerminal(dir, req.Cols, req.Rows)
	if err != nil {
		fail(w, 502, "terminal_failed", err)
		return
	}
	session := &terminal{id: id(), request: string(raw), file: f, cmd: cmd, done: make(chan struct{})}
	t.sessions[session.id], t.requests[req.RequestID] = session, session.id
	go session.run()
	respond(w, 201, session.description(t.service.bootID))
}

func (s *terminal) description(boot string) protocol.Terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "running"
	if s.ended {
		status = "ended"
	}
	return protocol.Terminal{ID: s.id, BootID: boot, Status: status}
}

func (s *terminal) run() {
	readDone := make(chan struct{})
	go func() { s.read(); close(readDone) }()
	_ = s.cmd.Wait()
	// Drain ordinary exit output, but do not let an inherited PTY keep the shell alive.
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
	}
	s.file.Close()
	<-readDone
	s.mu.Lock()
	s.ended = true
	if len(s.ring) > 16<<10 {
		s.ring = append([]byte(nil), s.ring[len(s.ring)-(16<<10):]...)
		s.truncated = true
	}
	if s.output != nil {
		close(s.output)
		s.output = nil
	}
	s.mu.Unlock()
	close(s.done)
}

func (s *terminal) read() {
	buffer := make([]byte, 16<<10)
	for {
		n, err := s.file.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			s.mu.Lock()
			s.ring = append(s.ring, chunk...)
			if len(s.ring) > 1<<20 {
				s.ring = append([]byte(nil), s.ring[len(s.ring)-(1<<20):]...)
				s.truncated = true
			}
			if s.output != nil {
				select {
				case s.output <- chunk:
				default:
					close(s.output)
					s.output = nil
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
}

func (t *terminals) list(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]protocol.Terminal, 0, len(t.sessions))
	for _, session := range t.sessions {
		result = append(result, session.description(t.service.bootID))
	}
	respond(w, 200, map[string]any{"terminals": result})
}

func (t *terminals) info(w http.ResponseWriter, r *http.Request) {
	s := t.lookup(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "terminal_lost", fmt.Errorf("terminal does not exist in this guest boot"))
		return
	}
	respond(w, 200, s.description(t.service.bootID))
}

func (t *terminals) end(w http.ResponseWriter, r *http.Request) {
	s := t.lookup(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "terminal_lost", fmt.Errorf("terminal does not exist in this guest boot"))
		return
	}
	s.terminate()
	respond(w, 200, s.description(t.service.bootID))
}

func (t *terminals) close() {
	t.mu.Lock()
	t.closed = true
	sessions := make([]*terminal, 0, len(t.sessions))
	for _, s := range t.sessions {
		sessions = append(sessions, s)
	}
	t.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() { defer wg.Done(); s.terminate() }()
	}
	wg.Wait()
}

func (t *terminals) attach(w http.ResponseWriter, r *http.Request) {
	s := t.lookup(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "terminal_lost", fmt.Errorf("terminal does not exist in this guest boot"))
		return
	}
	s.mu.Lock()
	if s.attached {
		s.mu.Unlock()
		fail(w, 409, "terminal_attached", fmt.Errorf("this terminal already has an active attachment"))
		return
	}
	s.attached = true
	output := make(chan []byte, 64)
	s.output = output
	replay, truncated, ended := append([]byte(nil), s.ring...), s.truncated, s.ended
	if ended {
		close(output)
		s.output = nil
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.attached = false
		if s.output == output {
			s.output = nil
		}
		s.mu.Unlock()
	}()
	// The host has authenticated the request with a per-machine secret. This API
	// is reachable only over vsock or the guest's private Unix socket.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(t.service.ctx, cancel)
	defer stop()
	write := func(kind websocket.MessageType, data []byte) error {
		timeout, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		return conn.Write(timeout, kind, data)
	}
	ready, _ := json.Marshal(map[string]any{"ready": true, "bootId": t.service.bootID, "replayTruncated": truncated})
	if write(websocket.MessageText, ready) != nil {
		return
	}
	if len(replay) > 0 && write(websocket.MessageBinary, replay) != nil {
		return
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		defer cancel()
		for {
			kind, b, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary {
				if err = s.file.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				if _, err = s.file.Write(b); err != nil {
					return
				}
			} else {
				var resize struct {
					Resize [2]uint16 `json:"resize"`
				}
				if json.Unmarshal(b, &resize) != nil || resize.Resize[0] < 2 || resize.Resize[0] > 500 || resize.Resize[1] < 1 || resize.Resize[1] > 200 {
					return
				}
				if s.resize(resize.Resize[0], resize.Resize[1]) != nil {
					return
				}
			}
		}
	}()
	defer func() { cancel(); conn.CloseNow(); <-inputDone }()
	for {
		select {
		case b, open := <-output:
			if !open {
				if s.description(t.service.bootID).Status == "ended" {
					_ = write(websocket.MessageText, []byte(`{"ended":true,"message":"Guest terminal ended."}`))
					_ = conn.Close(websocket.StatusNormalClosure, "Terminal ended")
				} else {
					_ = conn.Close(websocket.StatusTryAgainLater, "Output backlog exceeded; reconnect to replay")
				}
				return
			}
			if err = write(websocket.MessageBinary, b); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
