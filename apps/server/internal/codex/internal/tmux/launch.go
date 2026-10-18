package tmux

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadyOption marks completion of tmux setup for a particular run.
const ReadyOption = "@axv-codex-ready"

// Launch supplies process inputs independently of feature or storage models.
// OnSocket records the confirmed socket before any session setup proceeds.
type Launch struct {
	Target      Target
	RunID       string
	ProjectPath string
	Command     []string
	Environment []string
	Cols, Rows  int
	OnSocket    func(context.Context, string) error
}

// Values are transferred over stdin, never command arguments or a file. Encoding
// every byte avoids tmux expansion of dollar signs, formats and command syntax.
func quote(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(value); i++ {
		fmt.Fprintf(&out, "\\%03o", value[i])
	}
	out.WriteByte('"')
	return out.String()
}

func sourceCommand(source *strings.Builder, args ...string) {
	for i, arg := range args {
		if i != 0 {
			source.WriteByte(' ')
		}
		source.WriteString(quote(arg))
	}
	source.WriteByte('\n')
}

func (b Backend) Start(ctx context.Context, path string, launch Launch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := launch.Target
	setupRows := max(launch.Rows, 3)
	// The temporary pane prevents the runner from inheriting a stale shared-server
	// environment. Only this new session is configured before its real pane starts.
	output, err := b.commandInput(ctx, path, Environment(), "", Arguments(target.Socket,
		"new-session", "-d", "-E", "-P", "-F", "#{session_id}\t#{pane_id}\t#{socket_path}\t#{window_index}",
		"-s", target.Session, "-n", "Codex", "-c", literal(strings.ReplaceAll(launch.ProjectPath, "#", "##")),
		"-x", strconv.Itoa(launch.Cols), "-y", strconv.Itoa(setupRows), "/bin/sleep", "2147483647",
		";", "set-option", "-t", target.Session, "destroy-unattached", "off")...)
	if err != nil {
		return err
	}
	fields := strings.Split(output, "\t")
	if len(fields) != 4 || !strings.HasPrefix(fields[0], "$") || !strings.HasPrefix(fields[1], "%") || !filepath.IsAbs(fields[2]) {
		return fmt.Errorf("tmux did not confirm the new session identity")
	}
	sid, setupPane := fields[0], fields[1]
	if launch.OnSocket != nil {
		if err := launch.OnSocket(ctx, fields[2]); err != nil {
			return err
		}
	}
	target.Socket = fields[2]
	global, err := b.Command(ctx, path, target, "show-environment", "-g")
	if err != nil {
		return fmt.Errorf("could not inspect tmux environment names")
	}
	if len(global) >= (64<<10)-1 {
		return fmt.Errorf("tmux environment exceeds the supported inspection size")
	}
	var source strings.Builder
	// Remove inherited global values locally, including variables absent in AXV.
	for _, entry := range strings.Split(global, "\n") {
		key, _, _ := strings.Cut(strings.TrimPrefix(entry, "-"), "=")
		if validEnvironmentName(key) {
			sourceCommand(&source, "set-environment", "-r", "-t", sid, key)
		}
	}
	// Mask proxy variables even if another client adds them globally after the
	// snapshot. The owner's effective environment below wins locally.
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		sourceCommand(&source, "set-environment", "-r", "-t", sid, key)
	}
	for _, entry := range launch.Environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && validEnvironmentName(key) {
			sourceCommand(&source, "set-environment", "-t", sid, key, value)
		}
	}
	// new-session -x/-y records these in its environment after the first pane.
	// Removing them lets subsequent programs read the live PTY dimensions.
	sourceCommand(&source, "set-environment", "-r", "-t", sid, "COLUMNS")
	sourceCommand(&source, "set-environment", "-r", "-t", sid, "LINES")
	for _, option := range [][2]string{{"status", "off"}, {"prefix", "None"}, {"prefix2", "None"}, {"default-shell", "/bin/sh"}, {"mouse", "off"}, {"update-environment", ""}, {"base-index", "0"}, {"renumber-windows", "off"}, {"destroy-unattached", "off"}} {
		sourceCommand(&source, "set-option", "-t", sid, option[0], option[1])
	}
	for _, option := range [][2]string{{"remain-on-exit", "on"}, {"history-limit", "50000"}, {"window-size", "latest"}, {"pane-base-index", "0"}, {"automatic-rename", "off"}} {
		sourceCommand(&source, "set-window-option", "-t", sid+":", option[0], option[1])
	}
	if fields[3] != "0" {
		sourceCommand(&source, "move-window", "-s", sid+":"+fields[3], "-t", sid+":0")
	}
	args := append([]string{"split-window", "-d", "-f", "-t", setupPane, "-c", strings.ReplaceAll(launch.ProjectPath, "#", "##"), "--"}, launch.Command...)
	sourceCommand(&source, args...)
	sourceCommand(&source, "kill-pane", "-t", setupPane)
	sourceCommand(&source, "resize-window", "-t", sid+":0", "-x", strconv.Itoa(launch.Cols), "-y", strconv.Itoa(launch.Rows))
	sourceCommand(&source, "set-option", "-t", sid, "default-size", strconv.Itoa(launch.Cols)+"x"+strconv.Itoa(launch.Rows))
	sourceCommand(&source, "set-window-option", "-t", sid+":0", "window-size", "latest")
	sourceCommand(&source, "set-option", "-t", sid, ReadyOption, launch.RunID)
	_, err = b.commandInput(ctx, path, launch.Environment, source.String(), Arguments(target.Socket, "source-file", "-")...)
	return err
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// tmux treats a trailing semicolon as a command separator even with exec argv.
func literal(value string) string {
	if strings.HasSuffix(value, ";") {
		return strings.TrimSuffix(value, ";") + "\\;"
	}
	return value
}
