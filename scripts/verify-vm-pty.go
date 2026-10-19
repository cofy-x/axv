//go:build darwin || linux

// This native acceptance helper checks the actual system SSH client's terminal
// allocation, window-change transport, and behavior across a host interruption.
// It creates only its own process groups.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

func main() {
	args := os.Args[1:]
	ptyOnly := len(args) > 0 && args[0] == "--pty-only"
	if ptyOnly {
		args = args[1:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: verify-vm-pty [--pty-only] <axv executable> <machine ID>")
		os.Exit(1)
	}
	if err := verify(args[0], args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("SSH PTY allocation and resize passed (24x80 to 41x113).")
	if ptyOnly {
		return
	}
	if err := verifyInterruption(args[0], args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("SSH service interruption passed: stream detached, host restarted alone, VM boot preserved, and a fresh explicit connection succeeded.")
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Both the CLI and its proxy descendants belong to this probe's process group.
// Wait after escalation too; an unobserved SIGKILL is not completed cleanup.
func stopProbeGroup(pid int, done <-chan struct{}) error {
	return stopProbeGroupWith(pid, done, runtime.GOOS, syscall.Kill, 2*time.Second)
}

func stopProbeGroupWith(pid int, done <-chan struct{}, platform string, kill func(int, syscall.Signal) error, grace time.Duration) error {
	var permissionError error
	signalGroup := func(signal syscall.Signal) (bool, error) {
		err := kill(-pid, signal)
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		if platform == "darwin" && errors.Is(err, syscall.EPERM) {
			// Darwin can report EPERM while an exited child is being reaped.
			// Observe within the existing bound; permission never authorizes escalation.
			permissionError = err
			return true, nil
		}
		return err == nil, err
	}
	for _, step := range []struct {
		signal syscall.Signal
		limit  time.Duration
	}{{syscall.SIGTERM, grace}, {syscall.SIGKILL, grace}} {
		alive, err := signalGroup(step.signal)
		if err != nil {
			return err
		}
		if !alive {
			select {
			case <-done:
				return nil
			case <-time.After(time.Second):
				return errors.New("probe exited without being reaped")
			}
		}
		deadline := time.Now().Add(step.limit)
		for time.Now().Before(deadline) {
			alive, err := signalGroup(0)
			if err != nil {
				return err
			}
			if !alive {
				select {
				case <-done:
					return nil
				case <-time.After(time.Second):
					return errors.New("probe exited without being reaped")
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if permissionError != nil {
			return permissionError
		}
	}
	return fmt.Errorf("owned SSH probe process group %d did not stop", pid)
}

func verifyInterruption(executable, machine string) (result error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 2*time.Minute)
	defer timeout()
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
		cmd.WaitDelay = time.Second
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if cmd.Process != nil {
			done := make(chan struct{})
			close(done)
			err = errors.Join(err, stopProbeGroup(cmd.Process.Pid, done))
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w\n%s\n%s", strings.Join(args, " "), err, stderr.String(), output)
		}
		return output, nil
	}
	bootID := func() (string, error) {
		output, err := run("vm", "ip", machine, "--json")
		if err != nil {
			return "", err
		}
		var result struct {
			BootID string `json:"bootId"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return "", err
		}
		if result.BootID == "" {
			return "", errors.New("guest network response omitted its boot identity")
		}
		return result.BootID, nil
	}
	before, err := bootID()
	if err != nil {
		return err
	}
	stream := exec.CommandContext(ctx, executable, "vm", "ssh", "-T", machine, "--", "printf 'AXV_STREAM_READY\\n'; sleep 120")
	stream.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output synchronizedBuffer
	stream.Stdout, stream.Stderr = &output, &output
	if err := stream.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	var streamError error
	go func() { streamError = stream.Wait(); close(done) }()
	defer func() { result = errors.Join(result, stopProbeGroup(stream.Process.Pid, done)) }()
	ready := time.NewTimer(30 * time.Second)
	defer ready.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !strings.Contains(output.String(), "AXV_STREAM_READY") {
		select {
		case <-done:
			return fmt.Errorf("SSH exited before host interruption: %v\n%s", streamError, output.String())
		case <-ctx.Done():
			return ctx.Err()
		case <-ready.C:
			return fmt.Errorf("SSH did not announce its live stream\n%s", output.String())
		case <-tick.C:
		}
	}
	if _, err := run("daemon", "stop"); err != nil {
		return err
	}
	exited := time.NewTimer(20 * time.Second)
	defer exited.Stop()
	select {
	case <-done:
		if streamError == nil {
			return errors.New("SSH unexpectedly reported success after the host transport stopped")
		}
	case <-exited.C:
		return fmt.Errorf("SSH remained attached after its host stopped\n%s", output.String())
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := run("vm", "list", "--json"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("AXV_HOME"), "run", "desktop.json")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("host-only reactivation unexpectedly published a desktop runtime: %v", err)
	}
	after, err := bootID()
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("host interruption changed guest boot: %s to %s", before, after)
	}
	fresh, err := run("vm", "ssh", "-T", machine, "--", "printf 'AXV_FRESH_SSH'")
	if err != nil {
		return err
	}
	if string(fresh) != "AXV_FRESH_SSH" {
		return fmt.Errorf("fresh explicit SSH returned unexpected output: %q", fresh)
	}
	return nil
}

func verify(executable, machine string) (result error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 45*time.Second)
	defer timeout()
	command := "stty size; trap 'stty size; exit 0' WINCH; printf 'AXV_PTY_READY\\n'; while :; do sleep 1; done"
	cmd := exec.Command(executable, "vm", "ssh", "-t", "-t", machine, "--", command)
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return err
	}
	defer terminal.Close()
	done := make(chan struct{})
	var processError error
	go func() {
		processError = cmd.Wait()
		close(done)
	}()
	defer func() { result = errors.Join(result, stopProbeGroup(cmd.Process.Pid, done)) }()
	var mu sync.Mutex
	var output strings.Builder
	changed := make(chan struct{}, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buffer := make([]byte, 4096)
		for {
			n, readErr := terminal.Read(buffer)
			if n > 0 {
				mu.Lock()
				output.Write(buffer[:n])
				mu.Unlock()
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return output.String()
	}
	await := func(markers ...string) error {
		for {
			text := snapshot()
			found := true
			for _, marker := range markers {
				found = found && strings.Contains(text, marker)
			}
			if found {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("PTY probe: %w\n%s", ctx.Err(), snapshot())
			case <-changed:
			case <-done:
				select {
				case <-readDone:
				case <-ctx.Done():
					_ = terminal.Close()
					return fmt.Errorf("PTY output did not close after SSH exited: %w\n%s", ctx.Err(), snapshot())
				}
				text = snapshot()
				found = true
				for _, marker := range markers {
					found = found && strings.Contains(text, marker)
				}
				if found {
					return nil
				}
				err := processError
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return fmt.Errorf("SSH ended before terminal verification: %w\n%s", err, snapshot())
			}
		}
	}
	if err := await("24 80", "AXV_PTY_READY"); err != nil {
		return err
	}
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 41, Cols: 113}); err != nil {
		return err
	}
	if err := await("41 113"); err != nil {
		return err
	}
	select {
	case <-done:
		if processError != nil {
			return fmt.Errorf("SSH PTY command: %w\n%s", processError, snapshot())
		}
	case <-ctx.Done():
		return fmt.Errorf("SSH did not exit after terminal resize: %w\n%s", ctx.Err(), snapshot())
	}
	return nil
}
