// Package tmux owns the terminal backend for persistent host Codex runs.
// Targets are explicit runtime addresses, independent of storage records.
package tmux

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

// Target identifies one recorded tmux session. An empty socket uses the default
// server; a named socket uses -L and an absolute socket path uses -S.
type Target struct {
	Socket  string
	Session string
}

func (t Target) SessionTarget() string { return "=" + t.Session }
func (t Target) PaneTarget() string    { return t.SessionTarget() + ":0.0" }

// AttachCommand describes the exact recorded target for use in a host shell.
func (t Target) AttachCommand() string {
	args := []string{"tmux"}
	if t.Socket != "" {
		flag := "-L"
		if filepath.IsAbs(t.Socket) {
			flag = "-S"
		}
		args = append(args, flag, shellQuote(t.Socket))
	}
	args = append(args, "attach-session", "-t", shellQuote(t.SessionTarget()))
	return strings.Join(args, " ")
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Process is an attachment client; closing it never terminates its target run.
type Process interface {
	io.ReadWriter
	Resize(int, int) error
	Done() <-chan struct{}
	ExitError() error
	Close()
}

// Backend runs tmux commands against explicit targets. Run may be supplied by
// the owner to inject command execution; the zero value uses bounded commands.
type Backend struct {
	Run func(context.Context, string, ...string) (string, error)

	execute func(context.Context, string, []string, string, ...string) (string, error)
}

// Arguments preserves the distinction between default, named and path sockets.
func Arguments(socket string, args ...string) []string {
	result := []string{"-u"}
	if socket != "" {
		flag := "-L"
		if filepath.IsAbs(socket) {
			flag = "-S"
		}
		result = append(result, flag, socket)
	}
	return append(result, args...)
}

func (b Backend) Command(ctx context.Context, path string, target Target, args ...string) (string, error) {
	args = Arguments(target.Socket, args...)
	if b.Run != nil {
		return b.Run(ctx, path, args...)
	}
	return b.commandInput(ctx, path, Environment(filepath.Dir(path)), "", args...)
}

func (b Backend) commandInput(ctx context.Context, path string, env []string, input string, args ...string) (string, error) {
	if b.execute != nil {
		return b.execute(ctx, path, env, input, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env, cmd.Stdin = env, strings.NewReader(input)
	cmd.WaitDelay = 250 * time.Millisecond
	output := limitedbuffer.Buffer{Limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return strings.TrimSpace(string(output.Data)), err
}

// Environment removes inherited tmux routing and fixes terminal capabilities.
// Extra executable directories precede the caller's PATH.
func Environment(bin ...string) []string {
	result := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" && key != "TMUX_PANE" && key != "TERM" && key != "COLORTERM" && key != "PATH" {
			result = append(result, entry)
		}
	}
	return append(result, "PATH="+strings.Join(append(bin, os.Getenv("PATH")), string(os.PathListSeparator)), "TERM=xterm-256color", "COLORTERM=truecolor")
}
