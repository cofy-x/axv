package codex

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestCapabilityProbeDistinguishesMissingBrokenAndSupported(t *testing.T) {
	for _, tc := range []struct {
		name, platform, missing, broken, fail, help, reason string
		installed, ready                                    bool
	}{
		{name: "not installed", platform: "linux", missing: "codex", reason: "not installed"},
		{name: "Codex not executable", platform: "linux", broken: "codex", reason: "Codex CLI was found but is not executable", installed: true},
		{name: "tmux absent", platform: "linux", missing: "tmux", reason: "tmux is required", installed: true},
		{name: "tmux not executable", platform: "linux", broken: "tmux", reason: "tmux was found but is not executable", installed: true},
		{name: "unsupported host", platform: "windows", reason: "macOS or Linux", installed: true},
		{name: "Codex probe timeout", platform: "linux", fail: "codex --version", reason: "did not respond in time", installed: true},
		{name: "tmux probe timeout", platform: "linux", fail: "tmux -V", reason: "did not respond in time", installed: true},
		{name: "help failed", platform: "linux", fail: "codex --help", reason: "supported options", installed: true},
		{name: "older CLI", platform: "linux", help: "--cd", reason: "reliable conversation recovery", installed: true},
		{name: "no scrollback option", platform: "linux", help: "--cd --remote app-server", reason: "persistent terminal scrollback", installed: true},
		{name: "supported CLI", platform: "darwin", help: "--cd --no-alt-screen --no-daemon --remote app-server", installed: true, ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			find := func(name string) (string, bool) {
				if name == tc.missing {
					return "", false
				}
				return name, name != tc.broken
			}
			run := func(_ context.Context, path string, args ...string) (string, error) {
				if path+" "+strings.Join(args, " ") == tc.fail {
					return "", context.DeadlineExceeded
				}
				if args[0] == "--help" {
					return tc.help, nil
				}
				if path == "tmux" {
					return "tmux 3.6a", nil
				}
				return "codex-cli 0.162.0", nil
			}
			info := probeUsing(context.Background(), tc.platform, find, run)
			if info.Installed != tc.installed || info.Ready != tc.ready || !strings.Contains(info.Reason, tc.reason) || len(info.Guidance) == 0 {
				t.Fatalf("unexpected capability %+v", info)
			}
		})
	}
}

func TestProbeOutputIsBounded(t *testing.T) {
	var output cappedOutput
	data := []byte(strings.Repeat("x", 128<<10))
	if n, err := output.Write(data); err != nil || n != len(data) || output.Len() != 64<<10 {
		t.Fatalf("bounded output n=%d size=%d error=%v", n, output.Len(), err)
	}
	if _, err := fmt.Fprint(&output, "more"); err != nil || output.Len() != 64<<10 {
		t.Fatal("output exceeded limit")
	}
}

func TestConfirmedExitOutcomeRequiresValidWaitResult(t *testing.T) {
	for _, tc := range []struct {
		status, signal string
		confirmed      bool
	}{
		{status: "0", confirmed: true},
		{status: "7", confirmed: true},
		{status: "255", confirmed: true},
		{signal: "15", confirmed: true},
		{signal: "31", confirmed: true},
		{signal: "64", confirmed: runtime.GOOS != "darwin"},
		{signal: "TERM", confirmed: true},
		{signal: "term", confirmed: true},
		{signal: "SIGTERM", confirmed: true},
		{signal: "USR1", confirmed: true},
		{signal: "INFO", confirmed: true},
		{signal: "EMT", confirmed: true},
		{},
		{status: "256"},
		{status: "-1"},
		{status: "+0"},
		{status: "00"},
		{status: "unknown"},
		{status: "999999999999999999999"},
		{status: "0", signal: "TERM"},
		{signal: "0"},
		{signal: "01"},
		{signal: "256"},
		{signal: "65"},
		{signal: "127"},
		{signal: "200"},
		{signal: "TERM:extra"},
		{signal: "unknown"},
		{signal: "garbage"},
		{signal: "TERMextra"},
		{signal: "TERM\n"},
	} {
		if got := confirmedExitOutcome(tc.status, tc.signal); got != tc.confirmed {
			t.Errorf("confirmedExitOutcome(%q, %q) = %t, want %t", tc.status, tc.signal, got, tc.confirmed)
		}
	}
}

func TestTmuxVersionAndDefaultServerArguments(t *testing.T) {
	for _, version := range []string{"tmux 3.1", "tmux 3.1c", "tmux 3.6a", "tmux next-3.6", "tmux 4.0"} {
		if !supportedTmux(version) {
			t.Fatalf("rejected %q", version)
		}
	}
	for _, version := range []string{"tmux 2.9", "tmux 3.0a", "tmux unknown", "not tmux 3.6"} {
		if supportedTmux(version) {
			t.Fatalf("accepted %q", version)
		}
	}
	if got := strings.Join(tmuxArguments("", "list-sessions"), " "); got != "-u list-sessions" {
		t.Fatalf("default server was overridden: %s", got)
	}
	if got := strings.Join(tmuxArguments("/tmp/socket", "list-sessions"), " "); got != "-u -S /tmp/socket list-sessions" {
		t.Fatalf("saved socket was ignored: %s", got)
	}
}

func TestCodexVersionRequiresDurableEmptyHistorySupport(t *testing.T) {
	for _, version := range []string{"codex-cli 0.161.0", "codex-cli 0.161.0+build", "codex-cli 0.162.0", "codex-cli 0.162.0-alpha.6", "codex-cli 1.0.0"} {
		if !supportedCodex(version) {
			t.Fatalf("rejected %q", version)
		}
	}
	for _, version := range []string{"codex-cli 0.160.9", "codex-cli 0.161.0-alpha.1", "codex-cli unknown", "codex-cli test", "0.162.0", "codex-cli 0.162", "codex-cli 0.162.0-extra invalid", "codex-cli 999999999999999999999.0.0"} {
		if supportedCodex(version) {
			t.Fatalf("accepted %q", version)
		}
	}
}
