//go:build darwin || linux

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnershipWaitCancelsWithoutReplacingHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "conversation.lock")
	owner, err := AcquireOwnership(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if next, err := AcquireOwnership(ctx, path); !errors.Is(err, context.DeadlineExceeded) || next != nil {
		t.Fatalf("competing acquisition = %v, %v", next, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("ownership wait replaced the lock: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireOwnership(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestOwnershipRejectsUnsafeExistingPaths(t *testing.T) {
	for _, kind := range []string{"public-directory", "directory-link", "file-link", "public-file", "hard-linked-file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "private")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "conversation.lock")
			switch kind {
			case "public-directory":
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				link := filepath.Join(root, "link")
				if err := os.Symlink(directory, link); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(link, "conversation.lock")
			case "file-link":
				if err := os.Symlink(filepath.Join(root, "other"), path); err != nil {
					t.Fatal(err)
				}
			case "public-file":
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "hard-linked-file":
				other := filepath.Join(root, "other")
				if err := os.WriteFile(other, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(other, path); err != nil {
					t.Fatal(err)
				}
			}
			if lock, err := AcquireOwnership(context.Background(), path); !errors.Is(err, ErrUnavailable) || lock != nil {
				t.Fatalf("unsafe ownership path accepted: %v, %v", lock, err)
			}
		})
	}
}
