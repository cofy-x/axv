//go:build linux || darwin

package logs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func readEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err = json.Unmarshal(line, &event); err != nil {
			t.Fatalf("invalid JSON record: %s: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestPrivateAppendRedactionAndBoundedRecords(t *testing.T) {
	t.Setenv("AXV_TEST_SECRET", "fixture-environment-secret")
	t.Setenv("HTTPS_PROXY", "socks5://proxy-user:fixture-proxy-password@example.test:1080")
	home := t.TempDir()
	logger, err := Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	logger.With("jobId", "job-123", "attempt", 2, "token", "unknown-sensitive-value").Error("download.failed", fmt.Errorf("TLS certificate verify failed at https://user:fixture-password@example.test/a?signature=private-query#private-fragment; socks5://proxy-user:proxy-value@example.test:1080; proxy connection user:fixture-proxy-password; fixture-environment-secret; Authorization: Bearer bearer-not-known; Authorization: Basic basic-not-known"), "diagnostic", "first\nsecond", "long", strings.Repeat("x", 12<<10))
	second, err := Open(home, "host", nil)
	if err != nil {
		t.Fatal(err)
	}
	second.Info("service.start")
	events := readEvents(t, logger.Path())
	if len(events) != 2 || events[0]["jobId"] != "job-123" || events[0]["role"] != "desktop" || events[1]["role"] != "host" {
		t.Fatalf("wrong appended events: %+v", events)
	}
	b, _ := os.ReadFile(logger.Path())
	for _, secret := range []string{"fixture-environment-secret", "fixture-password", "private-query", "private-fragment", "proxy-value", "fixture-proxy-password", "unknown-sensitive-value", "bearer-not-known", "basic-not-known"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("secret %q persisted", secret)
		}
	}
	if !bytes.Contains(b, []byte("TLS certificate verify failed")) || !bytes.Contains(b, []byte("truncated")) {
		t.Fatal("useful failure cause or truncation marker lost")
	}
	for path, want := range map[string]os.FileMode{filepath.Join(home, "logs"): 0700, logger.Path(): 0600, filepath.Join(home, "logs", "daemon.lock"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("private mode %s: %v %v", path, info, err)
		}
	}
}

func TestRejectsUnsafeDirectoryFilesAndArchives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix ownership and links")
	}
	for _, target := range []string{"directory-link", "directory-mode", "log-link", "log-hardlink", "log-mode", "lock-link", "archive-link"} {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, "logs")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "daemon.log")
			switch target {
			case "directory-link":
				os.Remove(dir)
				if err := os.Symlink(filepath.Dir(outside), dir); err != nil {
					t.Fatal(err)
				}
			case "directory-mode":
				os.Chmod(dir, 0755)
			case "log-link":
				os.Symlink(outside, path)
			case "log-hardlink":
				os.Link(outside, path)
			case "log-mode":
				os.WriteFile(path, nil, 0644)
			case "lock-link":
				os.Symlink(outside, filepath.Join(dir, "daemon.lock"))
			case "archive-link":
				os.Symlink(outside, path+".1")
			}
			var fallback bytes.Buffer
			logger, err := Open(home, "desktop", &fallback)
			if target == "archive-link" {
				if err != nil {
					t.Fatal(err)
				}
				logger.sink.limit = 1
				logger.Info("one")
				logger.Info("two")
				if logger.Status() == "" || !strings.Contains(fallback.String(), "logging failed") {
					t.Fatal("unsafe archive accepted")
				}
			} else if err == nil {
				t.Fatal("unsafe sink initialized")
			}
			b, err := os.ReadFile(outside)
			if err != nil || string(b) != "preserved" {
				t.Fatalf("unrelated file changed: %q %v", b, err)
			}
		})
	}
}

func TestRotationBoundsAndRuntimeFallback(t *testing.T) {
	var fallback bytes.Buffer
	home := t.TempDir()
	logger, err := Open(home, "setup", &fallback)
	if err != nil {
		t.Fatal(err)
	}
	logger.sink.limit = 900
	for i := 0; i < 100; i++ {
		logger.Info("progress", "index", i, "detail", strings.Repeat("a", 100))
	}
	paths, err := filepath.Glob(logger.Path() + "*")
	if err != nil || len(paths) != archives+1 {
		t.Fatalf("archive bound: %v %v", paths, err)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.Size() > 900 {
			t.Fatalf("rotation size %s: %v %v", path, info, err)
		}
		readEvents(t, path)
	}
	if err := os.Rename(filepath.Join(home, "logs"), filepath.Join(home, "old-logs")); err != nil {
		t.Fatal(err)
	}
	logger.Info("committed.operation", "operationId", "op-kept")
	if logger.Status() == "" || !strings.Contains(fallback.String(), "op-kept") {
		t.Fatal("runtime sink failure not observable on stderr/status")
	}
	if err := os.Rename(filepath.Join(home, "old-logs"), filepath.Join(home, "logs")); err != nil {
		t.Fatal(err)
	}
	logger.Info("recovered")
	if logger.Status() != "" {
		t.Fatal("recovered sink retains failure status")
	}
}

func TestMultiProcessWriter(t *testing.T) {
	if home := os.Getenv("AXV_LOG_TEST_CHILD_HOME"); home != "" {
		logger, err := Open(home, "host", os.Stderr)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("AXV_LOG_TEST_ROTATE") != "" {
			logger.sink.limit = 2000
		}
		for i := 0; i < 80; i++ {
			logger.Info("concurrent", "worker", os.Getenv("AXV_LOG_TEST_CHILD_ID"), "index", i)
		}
		if logger.Status() != "" {
			t.Fatal(logger.Status())
		}
		return
	}
	for _, rotate := range []bool{false, true} {
		t.Run(fmt.Sprint(rotate), func(t *testing.T) {
			home := t.TempDir()
			logger, err := Open(home, "desktop", nil)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			outputs := make([][]byte, 4)
			errs := make([]error, 4)
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					cmd := exec.Command(os.Args[0], "-test.run=^TestMultiProcessWriter$")
					cmd.Env = append(os.Environ(), "AXV_LOG_TEST_CHILD_HOME="+home, "AXV_LOG_TEST_CHILD_ID="+strconv.Itoa(i))
					if rotate {
						cmd.Env = append(cmd.Env, "AXV_LOG_TEST_ROTATE=1")
					}
					outputs[i], errs[i] = cmd.CombinedOutput()
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("writer %d: %v: %s", i, err, outputs[i])
				}
			}
			paths, _ := filepath.Glob(logger.Path() + "*")
			seen := map[string]bool{}
			for _, path := range paths {
				for _, event := range readEvents(t, path) {
					key := fmt.Sprint(event["worker"], "/", event["index"])
					if seen[key] {
						t.Fatalf("duplicate event: %s", key)
					}
					seen[key] = true
				}
			}
			if !rotate && len(seen) != 320 {
				t.Fatalf("lost concurrent records: %d", len(seen))
			}
			if rotate && (len(paths) != archives+1 || len(seen) == 0) {
				t.Fatalf("concurrent rotation: %d paths, %d events", len(paths), len(seen))
			}
		})
	}
}

func TestTailIsBoundedAndDoesNotFollowLinks(t *testing.T) {
	home := t.TempDir()
	logger, err := Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		logger.Info("test", "index", i)
	}
	b, err := Tail(home, 400)
	if err != nil || len(b) > 400 {
		t.Fatalf("tail: %q %v", b, err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		if !json.Valid(line) {
			t.Fatalf("tail included partial line: %s", line)
		}
	}
	if err := os.Remove(logger.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "outside"), logger.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := Tail(home, 400); err == nil {
		t.Fatal("tail followed link")
	}
}
