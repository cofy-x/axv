//go:build darwin || linux

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func exitManifest(t *testing.T) (string, string) {
	t.Helper()
	directory, executable, _ := runnerFixture(t)
	manifest := Manifest{SessionID: "session", RunID: "run", ProjectPath: directory, CodexPath: executable, HistoryHome: directory, SocketRoot: filepath.Join(shortSocketHome(t), "run", "codex"), OwnershipPath: filepath.Join(directory, "ownership", "conversation.lock"), Mode: ModeNew}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return directory, path
}

func TestExitProofBindsExactManifestAndPreservesEstablishedOutcome(t *testing.T) {
	for _, code := range []int{0, 7, 255} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			directory, manifest := exitManifest(t)
			if proof, err := ReadExit(directory, "run"); proof != nil || err != nil {
				t.Fatalf("missing proof = %+v, %v", proof, err)
			}
			if err := RecordExit(manifest, code); err != nil {
				t.Fatal(err)
			}
			proof, err := ReadExit(directory, "run")
			if err != nil || proof == nil || proof.RunID != "run" || proof.ExitCode != code {
				t.Fatalf("proof = %+v, %v", proof, err)
			}
			path := filepath.Join(directory, "exit.json")
			info, err := os.Lstat(path)
			if err != nil || info.Mode().Perm() != 0o600 || !ownershipPrivate(info) {
				t.Fatalf("proof is not private: %v, %v", info, err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := RecordExit(manifest, (code+1)%256); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("duplicate publication = %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("established proof changed: %q, %v", after, err)
			}
			if proof, err := ReadExit(directory, "other-run"); proof != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("wrong run proof = %+v, %v", proof, err)
			}
		})
	}
}

func TestRunExitProofFollowsNormalCleanupAndWrapperOutcome(t *testing.T) {
	for _, item := range []struct {
		name string
		err  error
		code int
	}{
		{"success", nil, 0},
		{"failure", ErrProtocol, 1},
		{"cancelled", context.Canceled, 130},
	} {
		t.Run(item.name, func(t *testing.T) {
			directory, manifest := exitManifest(t)
			cleaned := false
			err := runWithExit(manifest, func() error {
				defer func() {
					if proof, err := ReadExit(directory, "run"); proof != nil || err != nil {
						t.Fatalf("proof appeared before cleanup completed: %+v, %v", proof, err)
					}
					cleaned = true
				}()
				return item.err
			})
			if !errors.Is(err, item.err) || !cleaned {
				t.Fatalf("cleanup/outcome changed: cleaned=%v error=%v", cleaned, err)
			}
			if proof, err := ReadExit(directory, "run"); err != nil || proof == nil || proof.ExitCode != item.code {
				t.Fatalf("wrapper proof = %+v, %v; want %d", proof, err, item.code)
			}
		})
	}
}

func TestRunPanicPropagatesAfterCleanupWithoutExitProof(t *testing.T) {
	directory, manifest := exitManifest(t)
	failure := errors.New("fixture panic")
	var recovered any
	cleaned := false
	func() {
		defer func() { recovered = recover() }()
		_ = runWithExit(manifest, func() error {
			defer func() { cleaned = true }()
			panic(failure)
		})
	}()
	if recovered != failure || !cleaned {
		t.Fatalf("panic or cleanup changed: panic=%v cleaned=%v", recovered, cleaned)
	}
	if proof, err := ReadExit(directory, "run"); proof != nil || err != nil {
		t.Fatalf("panic generated an exit proof: %+v, %v", proof, err)
	}
}

func TestRunExitPublicationFailureRemainsVisibleAndPreservesOriginalError(t *testing.T) {
	for _, original := range []error{nil, context.Canceled, ErrProtocol} {
		directory, manifest := exitManifest(t)
		path := filepath.Join(directory, "exit.json")
		if err := os.WriteFile(path, []byte("damaged established proof"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runWithExit(manifest, func() error { return original })
		if !errors.Is(err, ErrUnavailable) || (original != nil && !errors.Is(err, original)) || !strings.Contains(err.Error(), "record Codex runner exit") {
			t.Fatalf("publication failure hidden: %v", err)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != "damaged established proof" {
			t.Fatalf("publication failure replaced established state: %q, %v", data, readErr)
		}
	}
}

func TestRecordExitRejectsInvalidCodeAndManifestWithoutCreatingProof(t *testing.T) {
	for _, kind := range []string{"negative", "out-of-range", "damaged-manifest", "manifest-link"} {
		t.Run(kind, func(t *testing.T) {
			directory, manifest := exitManifest(t)
			code := 0
			switch kind {
			case "negative":
				code = -1
			case "out-of-range":
				code = 256
			case "damaged-manifest":
				if err := os.WriteFile(manifest, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "manifest-link":
				linked := filepath.Join(directory, "manifest-link.json")
				if err := os.Symlink(manifest, linked); err != nil {
					t.Fatal(err)
				}
				manifest = linked
			}
			if err := RecordExit(manifest, code); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid publication = %v", err)
			}
			if proof, err := ReadExit(directory, "run"); proof != nil || err != nil {
				t.Fatalf("invalid publication left a proof: %+v, %v", proof, err)
			}
		})
	}
}

func TestReadExitRejectsIncompleteAmbiguousAndOutOfRangeJSON(t *testing.T) {
	for _, data := range []string{
		`null`, `{}`, `[]`,
		`{"runId":"run"}`, `{"runId":"run","exitCode":null}`,
		`{"runId":"run","exitCode":"0"}`, `{"runId":"run","exitCode":0.5}`,
		`{"runId":"run","exitCode":-1}`, `{"runId":"run","exitCode":256}`,
		`{"runId":"wrong","exitCode":0}`, `{"runId":"","exitCode":0}`,
		`{"runId":"wrong","runId":"run","exitCode":0}`,
		`{"runId":"run","exitCode":7,"exitCode":0}`,
		`{"runId":"run","exitCode":0,"extra":true}`,
		`{"runId":"run","exitCode":0} {}`, `{"runId":"run","exitCode":0}x`,
		`{"runId":"run","exitCode":0` + strings.Repeat(" ", maxExitBytes),
	} {
		t.Run(data[:min(len(data), 60)], func(t *testing.T) {
			directory, _ := exitManifest(t)
			path := filepath.Join(directory, "exit.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if proof, err := ReadExit(directory, "run"); proof != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid proof accepted: %+v, %v", proof, err)
			}
		})
	}
}

func TestReadExitRejectsUnsafePathsWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"public-directory", "directory-link", "public-file", "file-link", "hard-link", "fifo", "foreign-owner"} {
		t.Run(kind, func(t *testing.T) {
			directory, _ := exitManifest(t)
			path := filepath.Join(directory, "exit.json")
			if err := os.WriteFile(path, []byte(`{"runId":"run","exitCode":0}`), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public-directory":
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				link := filepath.Join(t.TempDir(), "run-link")
				if err := os.Symlink(directory, link); err != nil {
					t.Fatal(err)
				}
				directory = link
			case "public-file":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "hard-link":
				if err := os.Link(path, filepath.Join(directory, "other")); err != nil {
					t.Fatal(err)
				}
			case "file-link", "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "file-link" {
					if err := os.Symlink(filepath.Join(directory, "missing"), path); err != nil {
						t.Fatal(err)
					}
				} else if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "foreign-owner":
				if os.Geteuid() != 0 {
					t.Skip("changing a fixture file's owner requires root")
				}
				if err := os.Chown(path, 65534, -1); err != nil {
					t.Fatal(err)
				}
			}
			if proof, err := ReadExit(directory, "run"); proof != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("unsafe proof accepted: %+v, %v", proof, err)
			}
		})
	}
}
