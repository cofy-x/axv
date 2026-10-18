//go:build darwin || linux

package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
)

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
