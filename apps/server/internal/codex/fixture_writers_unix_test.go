//go:build darwin || linux

package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A fixture holds its existing gate's lock across every owned file write. The
// parent can close a pending gate before a late process starts, without relying
// on mutable runner state, exit-proof visibility, pane liveness or PID reuse.
type fixtureWriters struct {
	directory string
	mu        sync.Mutex
	paths     []string
}

func newFixtureWriters(t *testing.T, directory string) *fixtureWriters {
	t.Helper()
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return &fixtureWriters{directory: directory}
}

func (writers *fixtureWriters) prepare(manifest string) (string, error) {
	path := filepath.Join(writers.directory, filepath.Base(filepath.Dir(manifest)))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write([]byte("P"))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", err
	}
	writers.mu.Lock()
	writers.paths = append(writers.paths, path)
	writers.mu.Unlock()
	return path, nil
}

func lockFixtureWriter(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Never create a gate: a late process cannot recreate a removed fixture.
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func fixtureWriterState(file *os.File) (byte, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 1 {
		return 0, fmt.Errorf("unsafe fixture writer gate")
	}
	var state [1]byte
	_, err = file.ReadAt(state[:], 0)
	return state[0], err
}

func setFixtureWriterState(file *os.File, state byte) error {
	n, err := file.WriteAt([]byte{state}, 0)
	if err == nil && n != 1 {
		err = io.ErrShortWrite
	}
	return err
}

func admitFixtureWriter(ctx context.Context, path string) (*os.File, error) {
	file, err := lockFixtureWriter(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := admitLockedFixtureWriter(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func admitLockedFixtureWriter(file *os.File) error {
	state, err := fixtureWriterState(file)
	if err == nil && state != 'P' {
		err = fmt.Errorf("fixture writer gate is closed: %c", state)
	}
	if err == nil {
		err = setFixtureWriterState(file, 'A')
	}
	return err
}

func finishFixtureWriter(file *os.File) error {
	return errors.Join(setFixtureWriterState(file, 'D'), file.Close())
}

func runWithFixtureWriter(ctx context.Context, gate string, run func() int) int {
	writer, err := admitFixtureWriter(ctx, gate)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 18
	}
	defer writer.Close()
	// A panic or Goexit releases the lock with A intact. Only a normally returned
	// body, including all of its cleanup defers, may confirm writer completion.
	code := run()
	if err := finishFixtureWriter(writer); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 18
	}
	return code
}

func removeFixtureDirectory(directory string, drained bool) error {
	if !drained {
		return fmt.Errorf("fixture writers did not complete; retained %s", directory)
	}
	return os.RemoveAll(directory)
}

func (writers *fixtureWriters) close(ctx context.Context) error {
	writers.mu.Lock()
	paths := append([]string(nil), writers.paths...)
	writers.mu.Unlock()
	var failures []error
	for _, path := range paths {
		file, err := lockFixtureWriter(ctx, path)
		if err == nil {
			state, stateErr := fixtureWriterState(file)
			err = stateErr
			if err == nil {
				switch state {
				case 'P', 'C':
					err = setFixtureWriterState(file, 'C')
				case 'D':
				case 'A':
					err = fmt.Errorf("fixture exited before completing its writers")
				default:
					err = fmt.Errorf("invalid fixture writer state: %q", state)
				}
			}
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("fixture %s: %w", filepath.Base(path), err))
		}
	}
	return errors.Join(failures...)
}

func TestFixtureWritersClosePendingAndRejectLateAdmission(t *testing.T) {
	root := t.TempDir()
	writers := newFixtureWriters(t, filepath.Join(root, "gates"))
	gate, err := writers.prepare(filepath.Join(root, "run", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A late child may already have opened this inode before teardown closes it.
	late, err := os.OpenFile(gate, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writers.close(ctx); err != nil {
		t.Fatalf("a runner that never started blocked cleanup: %v", err)
	}
	if writer, err := admitFixtureWriter(ctx, gate); err == nil {
		_ = finishFixtureWriter(writer)
		t.Fatal("late fixture was admitted after cleanup")
	}
	if err := os.RemoveAll(writers.directory); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(late.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if state, err := fixtureWriterState(late); err != nil || state != 'C' {
		t.Fatalf("a previously opened gate forgot its closed state: state=%q error=%v", state, err)
	}
	if err := admitLockedFixtureWriter(late); err == nil {
		t.Fatal("a previously opened inode admitted a late writer after removal")
	}
	if writer, err := admitFixtureWriter(ctx, gate); !errors.Is(err, os.ErrNotExist) {
		if writer != nil {
			_ = finishFixtureWriter(writer)
		}
		t.Fatalf("late fixture recreated a removed gate: %v", err)
	}
	if _, err := os.Stat(writers.directory); !os.IsNotExist(err) {
		t.Fatalf("late admission recreated the fixture directory: %v", err)
	}
}

func TestFixtureWritersWaitForLastWriteBeyondVisibleExitProof(t *testing.T) {
	root := t.TempDir()
	writers := newFixtureWriters(t, filepath.Join(root, "gates"))
	gate, err := writers.prepare(filepath.Join(root, "run", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := admitFixtureWriter(context.Background(), gate)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// A published proof or completion byte cannot bypass the still-held writer
	// lock. This forces the same window as RecordExit's remaining sync/defers.
	if err := os.WriteFile(filepath.Join(root, "exit.json"), []byte("visible"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setFixtureWriterState(writer, 'D'); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := writers.close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup passed a blocked final writer: %v", err)
	}
	if err := removeFixtureDirectory(root, false); err == nil {
		t.Fatal("incomplete fixture directory was removed")
	}
	last := filepath.Join(root, "last-write")
	if err := os.WriteFile(last, []byte("complete"), 0600); err != nil {
		t.Fatalf("cleanup deleted a still-active writer's directory: %v", err)
	}
	if err := finishFixtureWriter(writer); err != nil {
		t.Fatal(err)
	}
	if err := writers.close(context.Background()); err != nil {
		t.Fatalf("completed writer blocked cleanup: %v", err)
	}
	if data, err := os.ReadFile(last); err != nil || string(data) != "complete" {
		t.Fatalf("cleanup lost the final write: %v", err)
	}
	if err := removeFixtureDirectory(root, true); err != nil {
		t.Fatalf("completed fixture directory could not be removed: %v", err)
	}
}

func TestFixtureWritersDoNotConfirmPanicOrGoexit(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writers := newFixtureWriters(t, filepath.Join(root, "gates"))
			gate, err := writers.prepare(filepath.Join(root, "run", "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { _ = recover() }()
				runWithFixtureWriter(context.Background(), gate, func() int {
					if mode == "goexit" {
						runtime.Goexit()
					}
					panic("fixture stopped before completing its writers")
				})
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("abnormal fixture retained its gate lock")
			}
			if err := writers.close(context.Background()); err == nil || !strings.Contains(err.Error(), "before completing") {
				t.Fatalf("abnormal fixture was reported complete: %v", err)
			}
		})
	}
}

func TestFixtureWritersReportUnfinishedActiveState(t *testing.T) {
	root := t.TempDir()
	writers := newFixtureWriters(t, filepath.Join(root, "gates"))
	gate, err := writers.prepare(filepath.Join(root, "run", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := admitFixtureWriter(context.Background(), gate)
	if err != nil {
		t.Fatal(err)
	}
	// Model a fixture that died without its final completion defer.
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writers.close(context.Background()); err == nil || !strings.Contains(err.Error(), "before completing") {
		t.Fatalf("abnormal active fixture was silently accepted: %v", err)
	}
	if _, err := os.Stat(gate); err != nil {
		t.Fatalf("failed cleanup removed its diagnostic gate: %v", err)
	}
}

func TestFixtureWritersCompleteStartupFailureAndRequireAdmission(t *testing.T) {
	for _, withGate := range []bool{true, false} {
		t.Run(fmt.Sprint(withGate), func(t *testing.T) {
			root := t.TempDir()
			writers := newFixtureWriters(t, filepath.Join(root, "gates"))
			manifest := filepath.Join(root, "missing-run", "manifest.json")
			gate, err := writers.prepare(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-test.run=^TestCodexRunnerFixtureProcess$", "--"}
			want := 18
			if withGate {
				args = append(args, "--fixture-gate="+gate)
				want = 11
			}
			args = append(args, manifest)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, path, args...)
			command.Env = append(os.Environ(), "AXV_TEST_RUNNER=1", "AXV_TEST_RUNNER_GATE_REQUIRED=1")
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != want {
				t.Fatalf("fixture startup exit=%v output=%s; want %d", err, output, want)
			}
			if err := writers.close(ctx); err != nil {
				t.Fatalf("failed or unstarted fixture blocked cleanup: %v", err)
			}
		})
	}
}
