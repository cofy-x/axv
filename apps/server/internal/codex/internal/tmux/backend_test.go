package tmux

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestCommandsUseRecordedTarget(t *testing.T) {
	for _, tc := range []struct {
		socket string
		prefix []string
	}{
		{"", []string{"-u"}},
		{"recorded-server", []string{"-u", "-L", "recorded-server"}},
		{"/recorded/socket", []string{"-u", "-S", "/recorded/socket"}},
	} {
		t.Run(tc.socket, func(t *testing.T) {
			target := Target{Socket: tc.socket, Session: "recorded-session"}
			backend := Backend{Run: func(_ context.Context, path string, args ...string) (string, error) {
				want := append(slices.Clone(tc.prefix), "has-session", "-t", "=recorded-session")
				if path != "/bin/tmux" || !slices.Equal(args, want) {
					t.Fatalf("command used a different runtime address: path=%q args=%q", path, args)
				}
				return "recorded output", nil
			}}
			output, err := backend.Command(context.Background(), "/bin/tmux", target, "has-session", "-t", target.SessionTarget())
			if err != nil || output != "recorded output" || target.PaneTarget() != "=recorded-session:0.0" {
				t.Fatalf("target command failed: %q %v", output, err)
			}
		})
	}
}

func TestAttachCommandQuotesExactSocketAndSession(t *testing.T) {
	target := Target{Socket: "/owned/socket ' name", Session: "session ' $(false);"}
	want := `tmux -S '/owned/socket '"'"' name' attach-session -t '=session '"'"' $(false);'`
	if got := target.AttachCommand(); got != want {
		t.Fatalf("attach command = %q, want %q", got, want)
	}
	if got := (Target{Socket: "named", Session: "run"}).AttachCommand(); got != "tmux -L 'named' attach-session -t '=run'" {
		t.Fatalf("named socket attach command = %q", got)
	}
	if got := (Target{Session: "run"}).AttachCommand(); got != "tmux attach-session -t '=run'" {
		t.Fatalf("default socket attach command = %q", got)
	}
}

func TestEnvironmentRemovesInheritedRoutingAndKeepsHostValues(t *testing.T) {
	for key, value := range map[string]string{
		"TMUX": "/unrelated/socket,1,0", "TMUX_PANE": "%8", "PATH": "/host/bin",
		"TERM": "inherited-terminal", "COLORTERM": "inherited-color",
		"HTTPS_PROXY": "http://host-proxy:9000", "AXV_TMUX_TEST_VALUE": "literal value",
	} {
		t.Setenv(key, value)
	}
	values := make(map[string]string)
	for _, entry := range Environment("/runner/bin") {
		key, value, _ := strings.Cut(entry, "=")
		if _, exists := values[key]; exists {
			t.Fatalf("duplicate environment name %q", key)
		}
		values[key] = value
	}
	if _, exists := values["TMUX"]; exists {
		t.Fatal("inherited tmux routing retained")
	}
	if _, exists := values["TMUX_PANE"]; exists {
		t.Fatal("inherited tmux pane retained")
	}
	for key, want := range map[string]string{
		"PATH": "/runner/bin:/host/bin", "TERM": "xterm-256color", "COLORTERM": "truecolor",
		"HTTPS_PROXY": "http://host-proxy:9000", "AXV_TMUX_TEST_VALUE": "literal value",
	} {
		if values[key] != want {
			t.Fatalf("host environment changed %q", key)
		}
	}
}

func sourceCommands(t *testing.T, input string) [][]string {
	t.Helper()
	var commands [][]string
	for _, line := range strings.Split(strings.TrimSuffix(input, "\n"), "\n") {
		var args []string
		for _, token := range strings.Fields(line) {
			value, err := strconv.Unquote(token)
			if err != nil {
				t.Fatal("setup contained unquoted tmux syntax")
			}
			args = append(args, value)
		}
		commands = append(commands, args)
	}
	return commands
}

func TestStartRecordsSocketBeforeSessionLocalSetup(t *testing.T) {
	value := "quotes'\" $HOME #{session_name};\nsecond\\line\t世界"
	launch := Launch{
		Target: Target{Socket: "configured-socket", Session: "owned-run"}, RunID: "run-identity",
		ProjectPath: "/project/#{session_name};", Command: []string{"/runner", "literal ' argument"},
		Environment: []string{"HOST_VALUE=" + value, "HTTPS_PROXY=http://proxy:9191", "invalid-name=ignored"},
		Cols:        80, Rows: 1,
	}
	var recorded bool
	launch.OnSocket = func(_ context.Context, socket string) error {
		if socket != "/confirmed/socket" {
			t.Fatalf("recorded socket = %q", socket)
		}
		recorded = true
		return nil
	}
	var commands [][]string
	executions := 0
	backend := Backend{
		Run: func(_ context.Context, _ string, args ...string) (string, error) {
			if !recorded || !slices.Equal(args, []string{"-u", "-S", "/confirmed/socket", "show-environment", "-g"}) {
				t.Fatalf("environment inspection preceded recorded routing: %q", args)
			}
			return "STALE_VALUE=from-server\n-invalid-name=ignored\nOLD_VALUE=old", nil
		},
		execute: func(_ context.Context, _ string, env []string, input string, args ...string) (string, error) {
			executions++
			if slices.Contains(args, value) || slices.Contains(args, "HOST_VALUE="+value) {
				t.Fatal("environment value appeared in process arguments")
			}
			if executions == 1 {
				if input != "" || !slices.Equal(args[:3], []string{"-u", "-L", "configured-socket"}) || !slices.Contains(args, "/project/##{session_name}\\;") {
					t.Fatal("new session did not preserve configured routing and literal project path")
				}
				if slices.Contains(env, "HOST_VALUE="+value) {
					t.Fatal("runner environment populated the shared tmux server")
				}
				return "$4\t%9\t/confirmed/socket\t7", nil
			}
			if !recorded || !slices.Equal(args, []string{"-u", "-S", "/confirmed/socket", "source-file", "-"}) || !slices.Equal(env, launch.Environment) {
				t.Fatal("setup did not use recorded routing and the effective environment")
			}
			commands = sourceCommands(t, input)
			return "", nil
		},
	}
	if err := backend.Start(context.Background(), "/bin/tmux", launch); err != nil || executions != 2 {
		t.Fatalf("start failed: %v (executions=%d)", err, executions)
	}
	wanted := [][]string{
		{"set-environment", "-r", "-t", "$4", "STALE_VALUE"},
		{"set-environment", "-r", "-t", "$4", "OLD_VALUE"},
		{"set-environment", "-t", "$4", "HOST_VALUE", value},
		{"set-environment", "-t", "$4", "HTTPS_PROXY", "http://proxy:9191"},
		{"move-window", "-s", "$4:7", "-t", "$4:0"},
		{"split-window", "-d", "-f", "-t", "%9", "-c", "/project/##{session_name};", "--", "/runner", "literal ' argument"},
		{"resize-window", "-t", "$4:0", "-x", "80", "-y", "1"},
		{"set-option", "-t", "$4", ReadyOption, "run-identity"},
	}
	for _, want := range wanted {
		if !slices.ContainsFunc(commands, func(got []string) bool { return slices.Equal(got, want) }) {
			t.Fatalf("missing session setup command %q", want)
		}
	}
	for _, args := range commands {
		if slices.Contains(args, "-g") || slices.Contains(args, "invalid-name") || slices.Contains(args, "kill-server") {
			t.Fatal("session setup affected unrelated server state or accepted an invalid variable")
		}
	}
}

func TestStartStopsBeforeSetupWhenSocketCannotBeRecorded(t *testing.T) {
	recordErr := errors.New("recording unavailable")
	executions := 0
	backend := Backend{execute: func(context.Context, string, []string, string, ...string) (string, error) {
		executions++
		return "$1\t%2\t/confirmed/socket\t0", nil
	}, Run: func(context.Context, string, ...string) (string, error) {
		t.Fatal("setup continued after recording failed")
		return "", nil
	}}
	err := backend.Start(context.Background(), "/bin/tmux", Launch{OnSocket: func(context.Context, string) error { return recordErr }})
	if !errors.Is(err, recordErr) || executions != 1 {
		t.Fatalf("failed recording lost its boundary: %v executions=%d", err, executions)
	}
}

func TestStartRejectsUnconfirmedIdentityBeforeRecording(t *testing.T) {
	for _, output := range []string{"", "$1\t%2\trelative-socket\t0", "wrong\t%2\t/owned/socket\t0", "$1\twrong\t/owned/socket\t0"} {
		backend := Backend{execute: func(context.Context, string, []string, string, ...string) (string, error) {
			return output, nil
		}}
		err := backend.Start(context.Background(), "/bin/tmux", Launch{OnSocket: func(context.Context, string) error {
			t.Fatal("unconfirmed identity reached storage")
			return nil
		}})
		if err == nil {
			t.Fatal("unconfirmed identity accepted")
		}
	}
}

func TestStartRejectsTruncatedEnvironmentBeforeSetup(t *testing.T) {
	executions := 0
	backend := Backend{execute: func(context.Context, string, []string, string, ...string) (string, error) {
		executions++
		return "$1\t%2\t/owned/socket\t0", nil
	}, Run: func(context.Context, string, ...string) (string, error) {
		return strings.Repeat("A", (64<<10)-1), nil
	}}
	if err := backend.Start(context.Background(), "/bin/tmux", Launch{}); err == nil || executions != 1 {
		t.Fatalf("truncated environment accepted: %v executions=%d", err, executions)
	}
}

func TestScrollOnlyAddressesExplicitRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lines      int
		mode       string
		position   string
		wantCopy   bool
		operations []string
	}{
		{"leave live", 0, "0", "", false, []string{"display-message"}},
		{"leave history", 0, "1", "", false, []string{"display-message", "send-keys"}},
		{"open history", 12, "0", "", true, []string{"display-message", "copy-mode", "send-keys"}},
		{"later in live", -12, "0", "", false, []string{"display-message"}},
		{"later reaches live", -12, "1", "0", true, []string{"display-message", "send-keys", "display-message", "send-keys"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := Target{Socket: "/recorded/socket", Session: "old-run"}
			var operations []string
			backend := Backend{Run: func(_ context.Context, _ string, args ...string) (string, error) {
				if !slices.Equal(args[:3], []string{"-u", "-S", "/recorded/socket"}) || !slices.Contains(args, "=old-run:0.0") {
					t.Fatal("scroll retargeted a recorded run")
				}
				operations = append(operations, args[3])
				if slices.Contains(args, "#{scroll_position}") {
					return tc.position, nil
				}
				return tc.mode, nil
			}}
			copying, err := backend.Scroll(context.Background(), "/bin/tmux", target, tc.lines)
			if err != nil || copying != tc.wantCopy || !reflect.DeepEqual(operations, tc.operations) {
				t.Fatalf("scroll result: copying=%v operations=%q error=%v", copying, operations, err)
			}
		})
	}
}
