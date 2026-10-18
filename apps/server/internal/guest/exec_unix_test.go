//go:build linux || darwin

package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func fixtureArguments(t *testing.T, mode string, arguments ...string) []string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{executable, "-test.run=^TestGuestProcessFixture$", "--", mode}, arguments...)
}

// TestGuestProcessFixture runs only in an explicitly marked child process. The
// Go wrapper keeps the executable and all fixture data in the local test root.
func TestGuestProcessFixture(t *testing.T) {
	if os.Getenv("AXV_GUEST_PROCESS_FIXTURE") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || len(os.Args) <= separator+1 {
		os.Exit(2)
	}
	switch os.Args[separator+1] {
	case "exit":
		fmt.Fprint(os.Stdout, strings.Join(os.Args[separator+2:], "|"))
		fmt.Fprint(os.Stderr, "diagnostic")
		os.Exit(7)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("o", 70<<10))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 70<<10))
	case "wait":
		time.Sleep(30 * time.Second)
	case "terminal-output":
		fmt.Fprint(os.Stdout, strings.Repeat("x", (1<<20)+512)+"tail")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func execFixture(t *testing.T, s *service, mode string, timeout int, arguments ...string) (int, []byte) {
	t.Helper()
	request, err := json.Marshal(protocol.ExecRequest{Argv: fixtureArguments(t, mode, arguments...), TimeoutSeconds: timeout})
	if err != nil {
		t.Fatal(err)
	}
	response := guestRequest(s, "POST", "/v1/exec", string(request))
	return response.Code, response.Body.Bytes()
}

func TestExecPreservesArgumentsExitAndOutputBounds(t *testing.T) {
	t.Setenv("AXV_GUEST_PROCESS_FIXTURE", "1")
	s := testService(t, "exec")
	status, body := execFixture(t, s, "exit", 0, "$(not-a-shell)", "space value")
	var result protocol.ExecResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != 200 || result.ExitCode != 7 || result.Stdout != "$(not-a-shell)|space value" || result.Stderr != "diagnostic" || result.Truncated {
		t.Fatalf("unexpected result: %s", body)
	}
	status, body = execFixture(t, s, "large", 0)
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != 200 || result.ExitCode != 0 || len(result.Stdout) != 64<<10 || len(result.Stderr) != 64<<10 || !result.Truncated {
		t.Fatalf("output bounds = status %d, code %d, stdout %d, stderr %d, truncated %v", status, result.ExitCode, len(result.Stdout), len(result.Stderr), result.Truncated)
	}
	if got := len(s.executions); got != 0 {
		t.Fatalf("execution slots leaked: %d", got)
	}
}

func TestExecTimeoutTerminatesChildAndReleasesCapacity(t *testing.T) {
	t.Setenv("AXV_GUEST_PROCESS_FIXTURE", "1")
	s := testService(t, "exec")
	status, body := execFixture(t, s, "wait", 1)
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatal(err)
	}
	if status != 408 || problem.Code != "execution_timeout" || len(s.executions) != 0 {
		t.Fatalf("timeout result = %d %s, slots %d", status, body, len(s.executions))
	}
}

func TestEndedTerminalKeepsOnlyBoundedReplayTail(t *testing.T) {
	arguments := fixtureArguments(t, "terminal-output")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	cmd := exec.CommandContext(context.Background(), arguments[0], arguments[1:]...)
	cmd.Env = append(os.Environ(), "AXV_GUEST_PROCESS_FIXTURE=1")
	cmd.Stdout, cmd.Stderr = writer, io.Discard
	configureExecution(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	session := &terminal{id: "terminal-test", file: reader, cmd: cmd, done: make(chan struct{})}
	go session.run()
	t.Cleanup(session.terminate)
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal fixture did not finish")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.ended || !session.truncated || len(session.ring) != 16<<10 || !strings.HasSuffix(string(session.ring), "tail") {
		t.Fatalf("ended replay: ended %v, truncated %v, bytes %d", session.ended, session.truncated, len(session.ring))
	}
}
