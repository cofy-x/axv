//go:build darwin || linux

package terminal

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func realTerminal(t *testing.T) (*Manager, *httptest.Server, <-chan *unixProcess, string) {
	t.Helper()
	home := t.TempDir()
	// Do not read the user's shell profiles, HOME, shell choice, or session data.
	config := shellConfig{path: "/bin/sh", dir: home, env: []string{"HOME=" + home, "SHELL=/bin/sh", "PATH=/usr/bin:/bin", "TERM=xterm-256color", "LANG=en_US.UTF-8", "ENV=", "BASH_ENV="}}
	m := New()
	m.resolve = func() (shellConfig, error) { return config, nil }
	started := make(chan *unixProcess, 1)
	m.start = func(config shellConfig, cols, rows int) (process, error) {
		p, err := startProcess(config, cols, rows)
		if err == nil {
			started <- p.(*unixProcess)
		}
		return p, err
	}
	return m, testServer(t, m), started, home
}

func readOutputUntil(t *testing.T, conn *websocket.Conn, marker string) string {
	t.Helper()
	var output strings.Builder
	for !strings.Contains(output.String(), marker) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		kind, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("waiting for %q: %v; output=%q", marker, err, output.String())
		}
		if kind == websocket.MessageBinary {
			output.Write(data)
		}
		if output.Len() > 1<<20 {
			t.Fatal("unexpectedly large terminal output")
		}
	}
	return output.String()
}

func shellCommand(t *testing.T, conn *websocket.Conn, command string) {
	t.Helper()
	sendMessage(t, conn, websocket.MessageBinary, command+"\r")
}

func TestRealPTYInputOutputResizeAndNaturalExit(t *testing.T) {
	m, server, started, home := realTerminal(t)
	conn := dialTerminal(t, server, "?cols=96&rows=32")
	awaitReady(t, conn)
	p := <-started
	shellCommand(t, conn, `stty -echo; printf 'START_%s\n' ok`)
	readOutputUntil(t, conn, "START_ok")
	shellCommand(t, conn, `printf 'HOME_%s_END\n' "$PWD"; printf 'SIZE_%s_END\n' "$(stty size)"`)
	output := readOutputUntil(t, conn, "SIZE_32 96_END")
	if !strings.Contains(output, "HOME_"+home+"_END") {
		t.Fatalf("shell did not start in isolated home: %q", output)
	}
	sendMessage(t, conn, websocket.MessageText, `{"resize":[111,37]}`)
	shellCommand(t, conn, `printf 'SIZE_%s_END\n' "$(stty size)"; printf '\033[31mUTF8_你好\033[0m\n'`)
	output = readOutputUntil(t, conn, "UTF8_你好")
	if !strings.Contains(output, "SIZE_37 111_END") || !strings.Contains(output, "\x1b[31m") {
		t.Fatalf("resize or terminal bytes lost: %q", output)
	}
	shellCommand(t, conn, `printf 'BYE_%s\n' ok; exit`)
	var final strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			var closed websocket.CloseError
			if !errors.As(err, &closed) || closed.Code != websocket.StatusNormalClosure || closed.Reason != "Session ended" {
				t.Fatalf("exit close: %v", err)
			}
			break
		}
		if kind == websocket.MessageBinary {
			final.Write(data)
		}
	}
	if !strings.Contains(final.String(), "BYE_ok") {
		t.Fatalf("final output lost: %q", final.String())
	}
	assertReaped(t, p)
	waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 0 })
}

func TestProjectPTYStartsInCanonicalDirectoryAndKeepsHome(t *testing.T) {
	_, server, started, home := realTerminal(t)
	project := filepath.Join(home, "project's 中文; printf NOT_A_COMMAND")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	conn := dialTerminal(t, server, "?projectPath="+url.QueryEscape(project))
	awaitReady(t, conn)
	p := <-started
	if p.cmd.Dir != canonical || len(p.cmd.Args) != 2 || p.cmd.Args[1] != "-i" {
		t.Fatalf("project was not passed as a process working directory: dir=%q args=%q", p.cmd.Dir, p.cmd.Args)
	}
	shellCommand(t, conn, `stty -echo; printf 'START_%s\n' ok`)
	readOutputUntil(t, conn, "START_ok")
	shellCommand(t, conn, `printf 'PROJECT_%s_END\n' "$PWD"; printf 'HOME_%s_END\n' "$HOME"`)
	output := readOutputUntil(t, conn, "HOME_"+home+"_END")
	if !strings.Contains(output, "PROJECT_"+canonical+"_END") {
		t.Fatalf("shell did not start in the project directory: %q", output)
	}
	_ = conn.CloseNow()
	assertReaped(t, p)
}

func TestProjectDirectoryRejectsUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories without permission bits")
	}
	project := filepath.Join(t.TempDir(), "unreadable")
	if err := os.Mkdir(project, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(project, 0700) })
	if _, err := projectDirectory(project); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("unreadable project was accepted: %v", err)
	}
}

func assertReaped(t *testing.T, p *unixProcess) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("shell did not exit")
	}
	if err := syscall.Kill(p.cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("shell PID still exists after Wait: %v", err)
	}
}

func TestRealPTYDisconnectAndShutdownReapShell(t *testing.T) {
	for _, operation := range []string{"disconnect", "shutdown"} {
		t.Run(operation, func(t *testing.T) {
			m, server, started, _ := realTerminal(t)
			conn := dialTerminal(t, server, "")
			awaitReady(t, conn)
			p := <-started
			shellCommand(t, conn, `stty -echo; trap '' HUP; printf 'TRAP_%s\n' ready`)
			readOutputUntil(t, conn, "TRAP_ready")
			if operation == "disconnect" {
				_ = conn.CloseNow()
			} else {
				m.Close()
			}
			assertReaped(t, p)
			waitUntil(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active == 0 })
			p.Close() // Cleanup remains idempotent after a completed disconnect.
		})
	}
}

func TestDisconnectStopsForegroundJob(t *testing.T) {
	_, server, started, _ := realTerminal(t)
	conn := dialTerminal(t, server, "")
	awaitReady(t, conn)
	p := <-started
	shellCommand(t, conn, `stty -echo; printf 'JOB_%s\n' started; sleep 30`)
	readOutputUntil(t, conn, "JOB_started")
	foreground := 0
	waitUntil(t, func() bool {
		foreground = p.foregroundGroup()
		return foreground > 0 && foreground != p.cmd.Process.Pid
	})
	_ = conn.CloseNow()
	assertReaped(t, p)
	waitUntil(t, func() bool { return errors.Is(syscall.Kill(foreground, 0), syscall.ESRCH) })
}
