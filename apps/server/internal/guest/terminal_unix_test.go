//go:build linux || darwin

package guest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestTerminalDetachReplayAndExplicitEnd(t *testing.T) {
	// Match the test launcher's shell isolation while retaining production's
	// login Bash and PTY startup path.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ENV", "")
	t.Setenv("BASH_ENV", "")
	s := testService(t, "terminals")
	server := httptest.NewServer(s.handler())
	t.Cleanup(server.Close)
	body := `{"requestId":"terminal-request"}`
	created := guestRequest(s, "POST", "/v1/terminals", body)
	var terminalInfo protocol.Terminal
	if err := json.Unmarshal(created.Body.Bytes(), &terminalInfo); err != nil {
		t.Fatal(err)
	}
	if created.Code != 201 || terminalInfo.ID == "" || terminalInfo.BootID != s.bootID || terminalInfo.Status != "running" {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	// Defaults are part of the idempotent request identity.
	retry := guestRequest(s, "POST", "/v1/terminals", `{"requestId":"terminal-request","cols":80,"rows":24}`)
	var retried protocol.Terminal
	if err := json.Unmarshal(retry.Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if retry.Code != 200 || retried.ID != terminalInfo.ID {
		t.Fatalf("retry = %d %s", retry.Code, retry.Body)
	}
	assertProblem(t, guestRequest(s, "POST", "/v1/terminals", `{"requestId":"terminal-request","rows":25}`), 409, "idempotency_conflict")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	path := "/v1/terminals/" + terminalInfo.ID
	attach := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+path+"/attach", &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + s.config.Token}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ready struct {
			Ready  bool   `json:"ready"`
			BootID string `json:"bootId"`
		}
		if json.Unmarshal(data, &ready) != nil || kind != websocket.MessageText || !ready.Ready || ready.BootID != s.bootID {
			t.Fatalf("attachment readiness = %s", data)
		}
		return conn
	}
	readUntil := func(conn *websocket.Conn, marker string) {
		t.Helper()
		var output strings.Builder
		for !strings.Contains(output.String(), marker) {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("read terminal output: %v; output %q", err, output.String())
			}
			if kind == websocket.MessageBinary {
				output.Write(data)
			}
		}
	}
	session := s.terminals.lookup(terminalInfo.ID)
	waitDetached := func() {
		t.Helper()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			session.mu.Lock()
			attached := session.attached
			session.mu.Unlock()
			if !attached {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("terminal did not detach")
			case <-ticker.C:
			}
		}
	}

	conn := attach()
	// Octal escapes prevent the echoed input from satisfying the output check.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf '\\141\\170\\166\\055\\147\\165\\145\\163\\164\\055\\162\\145\\160\\154\\141\\171\\n'\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(conn, "axv-guest-replay")
	assertProblem(t, guestRequest(s, "GET", path+"/attach", ""), 409, "terminal_attached")
	conn.CloseNow()
	waitDetached()
	if got := session.description(s.bootID).Status; got != "running" {
		t.Fatalf("disconnect ended the terminal: %s", got)
	}

	conn = attach()
	readUntil(conn, "axv-guest-replay")
	conn.CloseNow()
	waitDetached()
	ended := guestRequest(s, "DELETE", path, "")
	if ended.Code != 200 || session.description(s.bootID).Status != "ended" {
		t.Fatalf("explicit end = %d %s", ended.Code, ended.Body)
	}
	if again := guestRequest(s, "DELETE", path, ""); again.Code != 200 {
		t.Fatalf("repeated end = %d %s", again.Code, again.Body)
	}
	conn = attach()
	readUntil(conn, "axv-guest-replay")
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || !strings.Contains(string(data), `"ended":true`) {
		t.Fatalf("ended attachment = %v %s, %v", kind, data, err)
	}
	conn.CloseNow()
	assertProblem(t, guestRequest(s, "GET", "/v1/terminals/missing", ""), 404, "terminal_lost")
}

func TestTerminalValidationAndClosedRegistry(t *testing.T) {
	s := testService(t, "terminals")
	for _, body := range []string{
		`{"requestId":"short"}`, `{"requestId":"terminal-test","cols":1}`, `{"requestId":"terminal-test","cols":501}`,
		`{"requestId":"terminal-test","rows":201}`, `{"requestId":"terminal-test","extra":true}`,
	} {
		assertProblem(t, guestRequest(s, "POST", "/v1/terminals", body), 400, "invalid_request")
	}
	s.terminals.close()
	assertProblem(t, guestRequest(s, "POST", "/v1/terminals", `{"requestId":"terminal-test"}`), 503, "terminal_limit")
}
