//go:build darwin || linux

package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttachmentStartFailurePreservesStageAndNativeCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-tmux")
	p, err := (Backend{}).Attach(path, Target{Session: "exact-run"}, 80, 24)
	if p != nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "start attachment PTY") {
		t.Fatalf("attachment lost its startup stage or native cause: process=%v error=%v", p, err)
	}
}

func TestNativeCommandBoundsOutputAndHonorsCallerDeadline(t *testing.T) {
	backend := Backend{}
	output, err := backend.commandInput(context.Background(), "/bin/sh", Environment(), "", "-c", `i=0; while [ "$i" -lt 20000 ]; do printf 'abcdefgh'; i=$((i+1)); done`)
	if err != nil || len(output) != 64<<10 || !strings.HasPrefix(output, "abcdefgh") {
		t.Fatalf("command output was not bounded: len=%d error=%v", len(output), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := backend.commandInput(ctx, "/bin/sleep", Environment(), "", "30"); err == nil {
		t.Fatal("command ignored caller deadline")
	}
	if time.Since(started) > time.Second {
		t.Fatal("command did not return promptly after deadline")
	}
}
