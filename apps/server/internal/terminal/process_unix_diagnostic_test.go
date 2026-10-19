//go:build darwin || linux

package terminal

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type processDiagnosticLog struct {
	mu          sync.Mutex
	lines       []string
	gate        chan struct{}
	entered     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func captureProcessDiagnostics(t *testing.T, blocked bool) *processDiagnosticLog {
	t.Helper()
	// These tests are sequential because the standard logger is process-wide.
	output := &processDiagnosticLog{}
	if blocked {
		output.gate = make(chan struct{})
		output.entered = make(chan struct{})
	}
	previous := log.Writer()
	log.SetOutput(output)
	t.Cleanup(func() {
		output.release()
		log.SetOutput(previous)
	})
	return output
}

func (output *processDiagnosticLog) Write(data []byte) (int, error) {
	if output.gate != nil {
		output.enterOnce.Do(func() { close(output.entered) })
		<-output.gate
	}
	output.mu.Lock()
	output.lines = append(output.lines, string(data))
	output.mu.Unlock()
	return len(data), nil
}

func (output *processDiagnosticLog) release() {
	if output.gate != nil {
		output.releaseOnce.Do(func() { close(output.gate) })
	}
}

func (output *processDiagnosticLog) line(pid int) string {
	output.mu.Lock()
	defer output.mu.Unlock()
	for _, line := range output.lines {
		if strings.Contains(line, fmt.Sprintf("pid=%d ", pid)) {
			return line
		}
	}
	return ""
}

func assertProcessDiagnostic(t *testing.T, output *processDiagnosticLog, p *unixProcess, requested bool, exitStatus string) {
	t.Helper()
	assertReaped(t, p)
	var line string
	waitUntil(t, func() bool {
		line = output.line(p.cmd.Process.Pid)
		return line != ""
	})
	fields := fmt.Sprintf("host terminal process exited pid=%d shell=%q ", p.cmd.Process.Pid, p.cmd.Path)
	closeState := fmt.Sprintf(" close_requested=%t wait_error=", requested)
	if !strings.Contains(line, fields) || !strings.Contains(line, exitStatus) || !strings.Contains(line, " elapsed_ms=") || !strings.Contains(line, closeState) {
		t.Fatalf("process diagnostic lost its exit metadata: %q", line)
	}
	if strings.Contains(line, "START_ok") || strings.Contains(line, "HOME=") || strings.Contains(line, "stty") {
		t.Fatalf("process diagnostic retained shell input, output or environment: %q", line)
	}
}

func TestRealPTYExitDiagnostics(t *testing.T) {
	for _, tc := range []struct{ action, exitStatus string }{
		{"exit-zero", "exit_code=0 signal=0"},
		{"exit-nonzero", "exit_code=17 signal=0"},
		{"signal", fmt.Sprintf("exit_code=-1 signal=%d", syscall.SIGKILL)},
		{"close", ""},
	} {
		t.Run(tc.action, func(t *testing.T) {
			output := captureProcessDiagnostics(t, false)
			_, server, started, _ := realTerminal(t)
			conn := dialTerminal(t, server, "")
			awaitReady(t, conn)
			p := <-started
			shellCommand(t, conn, `stty -echo; printf 'START_%s\n' ok`)
			readOutputUntil(t, conn, "START_ok")
			switch tc.action {
			case "exit-zero":
				shellCommand(t, conn, "exit 0")
			case "exit-nonzero":
				shellCommand(t, conn, "exit 17")
			case "signal":
				if err := p.cmd.Process.Signal(syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			case "close":
				p.Close()
			}
			assertProcessDiagnostic(t, output, p, tc.action == "close", tc.exitStatus)
		})
	}
}

func TestProcessDiagnosticCannotDelayDoneOrReclassifyNaturalExit(t *testing.T) {
	output := captureProcessDiagnostics(t, true)
	_, server, started, _ := realTerminal(t)
	t.Cleanup(output.release) // Unblock logging before the server cleanup, even on failure.
	conn := dialTerminal(t, server, "")
	awaitReady(t, conn)
	p := <-started
	shellCommand(t, conn, "exit 0")
	select {
	case <-output.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not reach diagnostic logging")
	}
	assertReaped(t, p)
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for diagnostic logging")
	}
	if !p.closeRequested.Load() {
		t.Fatal("natural-exit cleanup did not mark its Close request")
	}
	output.release()
	assertProcessDiagnostic(t, output, p, false, "exit_code=0 signal=0")
}

func TestTwoRealPTYsKeepTheirOwnProcessAndSize(t *testing.T) {
	m, server, started, _ := realTerminal(t)
	first := dialTerminal(t, server, "?cols=80&rows=24")
	awaitReady(t, first)
	firstProcess := <-started
	shellCommand(t, first, `stty -echo; printf 'FIRST_%s\n' ready`)
	readOutputUntil(t, first, "FIRST_ready")
	for iteration := range 3 {
		second := dialTerminal(t, server, "?cols=96&rows=32")
		awaitReady(t, second)
		secondProcess := <-started
		if firstProcess.cmd.Process.Pid == secondProcess.cmd.Process.Pid {
			t.Fatal("two terminals reused one process")
		}
		command := fmt.Sprintf("stty -echo; printf 'SECOND_%%s\\n' %d\r", iteration)
		for _, character := range command {
			sendMessage(t, second, websocket.MessageBinary, string(character))
		}
		readOutputUntil(t, second, fmt.Sprintf("SECOND_%d", iteration))
		sendMessage(t, second, websocket.MessageText, `{"resize":[111,37]}`)
		shellCommand(t, second, `printf 'SIZE_%s_END\n' "$(stty size)"`)
		readOutputUntil(t, second, "SIZE_37 111_END")
		if iteration == 0 {
			shellCommand(t, second, "exit 0")
		} else {
			_ = second.CloseNow()
		}
		assertReaped(t, secondProcess)
		_ = second.CloseNow()
		waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 1 })
		shellCommand(t, first, fmt.Sprintf("printf 'ALIVE_%%s_END\\n' %d; printf 'SIZE_%%s_END\\n' \"$(stty size)\"", iteration))
		output := readOutputUntil(t, first, "SIZE_24 80_END")
		if !strings.Contains(output, fmt.Sprintf("ALIVE_%d_END", iteration)) {
			t.Fatalf("the first terminal stopped responding after its peer closed: %q", output)
		}
		select {
		case <-firstProcess.Done():
			t.Fatal("closing a second terminal ended the first shell")
		default:
		}
	}
	_ = first.CloseNow()
	assertReaped(t, firstProcess)
	waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 0 })
}
