//go:build darwin || linux

package nativepty

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

func sleepingPTY(t *testing.T) *os.File {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "stty -echo -icanon; printf ready; exec sleep 30")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"HOME=" + cmd.Dir, "PATH=/usr/bin:/bin", "ENV=", "BASH_ENV="}
	file, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = file.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if err := file.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, len("ready"))
	if _, err := io.ReadFull(file, ready); err != nil || string(ready) != "ready" {
		t.Fatalf("shell readiness: %q, %v", ready, err)
	}
	if err := file.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return file
}

func ioResult(t *testing.T, operation func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	return done
}

func awaitIO(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("PTY I/O did not unblock")
		return nil
	}
}

func TestDeadlinesSurviveResize(t *testing.T) {
	file := sleepingPTY(t)
	if err := file.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	// The dependency must preserve pollability when it performs an ioctl.
	if err := pty.Setsize(file, &pty.Winsize{Cols: 111, Rows: 37}); err != nil {
		t.Fatal(err)
	}
	err := awaitIO(t, ioResult(t, func() error { _, err := file.Read(make([]byte, 1)); return err }))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read after resize: %v", err)
	}
	if err := file.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := file.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	err = awaitIO(t, ioResult(t, func() error {
		_, err := file.Write(bytes.Repeat([]byte{'x'}, 1<<20))
		return err
	}))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write to a process that does not read: %v", err)
	}
}

func TestCloseUnblocksRead(t *testing.T) {
	file := sleepingPTY(t)
	started := make(chan struct{})
	done := ioResult(t, func() error {
		close(started)
		_, err := file.Read(make([]byte, 1))
		return err
	})
	<-started
	select {
	case err := <-done:
		t.Fatalf("read completed before close: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := awaitIO(t, done); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
}
