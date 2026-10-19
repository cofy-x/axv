//go:build darwin || linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestPTYProbeCleanupWaitsAfterEscalation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestPTYProbeStubbornChild$")
	cmd.Env = append(os.Environ(), "AXV_PTY_PROBE_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	})
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "READY\n" {
			t.Fatalf("unexpected child output: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
	if err = stopProbeGroup(cmd.Process.Pid, done); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("cleanup returned before reaping the SSH probe")
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("owned process group survived cleanup: %v", err)
	}
}

func TestPTYProbeStubbornChild(t *testing.T) {
	if os.Getenv("AXV_PTY_PROBE_CHILD") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Println("READY")
	for {
		time.Sleep(time.Hour)
	}
}

func TestPTYProbeDarwinGroupPermissionObservation(t *testing.T) {
	done := make(chan struct{})
	close(done)
	observed := 0
	transient := func(_ int, signal syscall.Signal) error {
		if signal == syscall.SIGKILL {
			t.Fatal("permission failure authorized escalation")
		}
		observed++
		if observed <= 2 {
			return syscall.EPERM
		}
		return syscall.ESRCH
	}
	if err := stopProbeGroupWith(123, done, "darwin", transient, time.Second); err != nil {
		t.Fatalf("transient Darwin EPERM was not observed through reap: %v", err)
	}
	if observed != 3 {
		t.Fatalf("unexpected signal observations: %d", observed)
	}
	persistent := func(_ int, signal syscall.Signal) error {
		if signal == syscall.SIGKILL {
			t.Fatal("permission failure authorized escalation")
		}
		return syscall.EPERM
	}
	if err := stopProbeGroupWith(123, done, "darwin", persistent, 20*time.Millisecond); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("persistent Darwin permission error was hidden: %v", err)
	}
	calls := 0
	if err := stopProbeGroupWith(123, done, "linux", func(int, syscall.Signal) error { calls++; return syscall.EPERM }, time.Second); !errors.Is(err, syscall.EPERM) || calls != 1 {
		t.Fatalf("Linux permission error was not returned immediately: calls=%d error=%v", calls, err)
	}
}
