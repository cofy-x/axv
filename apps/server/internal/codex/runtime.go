package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type Info struct {
	Supported          bool     `json:"supported"`
	Installed          bool     `json:"installed"`
	Ready              bool     `json:"ready"`
	CodexPath          string   `json:"codexPath,omitempty"`
	CodexVersion       string   `json:"codexVersion,omitempty"`
	TmuxPath           string   `json:"tmuxPath,omitempty"`
	TmuxVersion        string   `json:"tmuxVersion,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	Guidance           []string `json:"guidance"`
	DefaultProjectPath string   `json:"defaultProjectPath"`
	ConfigPath         string   `json:"configPath"`
	Network            Network  `json:"network"`
}

type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if available := (64 << 10) - b.Len(); available > 0 {
		if len(p) > available {
			p = p[:available]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func command(ctx context.Context, path string, args ...string) (string, error) {
	return commandEnvironment(ctx, path, hostEnvironment(filepath.Dir(path)), args...)
}

func commandEnvironment(ctx context.Context, path string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.WaitDelay = 250 * time.Millisecond
	var output cappedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return strings.TrimSpace(output.String()), err
}

func hostEnvironment(bin ...string) []string { return tmux.Environment(bin...) }

func executableCandidates(name string) []string {
	home, _ := os.UserHomeDir()
	dirs := append(filepath.SplitList(os.Getenv("PATH")), []string{os.Getenv("NVM_BIN"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".volta", "bin"), filepath.Join(home, ".asdf", "shims"), filepath.Join(home, ".local", "share", "mise", "shims"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}...)
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
	sort.Sort(sort.Reverse(sort.StringSlice(nvm)))
	dirs = append(dirs, nvm...)
	result := []string{}
	for _, dir := range dirs {
		if dir != "" && filepath.IsAbs(dir) {
			result = append(result, filepath.Join(dir, name))
		}
	}
	return result
}

func executable(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		if absolute, err := filepath.Abs(path); err == nil {
			return absolute
		}
	}
	for _, path := range executableCandidates(name) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}

func unavailableExecutable(name string) string {
	for _, path := range executableCandidates(name) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 == 0 {
			return path
		}
	}
	return ""
}

func probe(ctx context.Context) Info {
	return probeUsing(ctx, runtime.GOOS, func(name string) (string, bool) {
		if path := executable(name); path != "" {
			return path, true
		}
		return unavailableExecutable(name), false
	}, command)
}

func probeUsing(ctx context.Context, platform string, find func(string) (string, bool), run func(context.Context, string, ...string) (string, error)) Info {
	info := Info{Supported: platform == "darwin" || platform == "linux", Guidance: []string{}}
	var codexRunnable, tmuxRunnable bool
	info.CodexPath, codexRunnable = find("codex")
	info.TmuxPath, tmuxRunnable = find("tmux")
	info.Installed = info.CodexPath != ""
	if info.Installed && !codexRunnable {
		info.Reason = "The Codex CLI was found but is not executable by the host user."
		info.Guidance = append(info.Guidance, "Repair or reinstall the official Codex CLI, or restore executable permissions to its file, then choose Check Again.")
		return info
	}
	if info.TmuxPath != "" && !tmuxRunnable {
		info.Reason = "tmux was found but is not executable by the host user."
		info.Guidance = append(info.Guidance, "Repair or reinstall tmux, or restore executable permissions to its file, then choose Check Again.")
		return info
	}
	if !info.Supported {
		info.Reason = "Host Codex sessions currently require macOS or Linux with tmux."
		info.Guidance = append(info.Guidance, "Run the AXV Go service on a macOS or Linux host.")
		return info
	}
	if !info.Installed {
		info.Reason = "The official Codex CLI is not installed."
		info.Guidance = append(info.Guidance, "Install the official Codex CLI with npm install -g @openai/codex, then choose Check Again.")
	}
	if info.TmuxPath == "" {
		info.Reason = "tmux is required to keep Codex running when its window closes."
		info.Guidance = append(info.Guidance, "Install tmux: brew install tmux on macOS, or your Linux package manager (for example sudo apt install tmux).")
	}
	if !info.Installed || info.TmuxPath == "" {
		return info
	}
	var err error
	info.CodexVersion, err = run(ctx, info.CodexPath, "--version")
	if err != nil {
		info.Reason = "The Codex executable could not run or did not respond in time."
		info.Guidance = append(info.Guidance, "Run codex --version in the host Terminal and repair the installation, then choose Check Again.")
		return info
	}
	if !supportedCodex(info.CodexVersion) {
		info.Reason = "Codex CLI 0.161.0 or later is required to preserve resumable conversations."
		info.Guidance = append(info.Guidance, "Update the official Codex CLI (npm install -g @openai/codex), confirm codex --version reports a supported release, then choose Check Again.")
		return info
	}
	info.TmuxVersion, err = run(ctx, info.TmuxPath, "-V")
	if err != nil {
		info.Reason = "The tmux executable could not run or did not respond in time."
		info.Guidance = append(info.Guidance, "Run tmux -V in the host Terminal and repair the installation, then choose Check Again.")
		return info
	}
	if !supportedTmux(info.TmuxVersion) {
		info.Reason = "tmux 3.1 or later is required for reliable session startup."
		info.Guidance = append(info.Guidance, "Update tmux using the host package manager, then choose Check Again.")
		return info
	}
	help, err := run(ctx, info.CodexPath, "--help")
	if err != nil {
		info.Reason = "Codex could not report its supported options."
		info.Guidance = append(info.Guidance, "Run codex --help in the host Terminal and repair the installation.")
		return info
	}
	if !strings.Contains(help, "--remote") || !strings.Contains(help, "app-server") {
		info.Reason = "This Codex version cannot provide reliable conversation recovery."
		info.Guidance = append(info.Guidance, "Update the official Codex CLI (npm install -g @openai/codex) to a version supporting --remote and app-server, then choose Check Again.")
		return info
	}
	if !strings.Contains(help, "--no-alt-screen") {
		info.Reason = "This Codex version cannot provide persistent terminal scrollback."
		info.Guidance = append(info.Guidance, "Update the official Codex CLI (npm install -g @openai/codex) to a version supporting --no-alt-screen, then choose Check Again.")
		return info
	}
	info.Ready = true
	info.Guidance = append(info.Guidance, "Codex uses the host user's existing login and configuration. If needed, run codex login in Terminal.")
	return info
}

func instanceID(databaseID, directory string) string {
	hash := sha256.Sum256([]byte(databaseID + "\x00" + directory))
	return fmt.Sprintf("%x", hash[:12])
}

func runtimeTarget(session storage.CodexSession) tmux.Target {
	return tmux.Target{Socket: session.TmuxSocket, Session: session.TmuxSession}
}
func target(session storage.CodexSession) string { return runtimeTarget(session).SessionTarget() }
func (m *Manager) backend() tmux.Backend {
	return tmux.Backend{Run: func(ctx context.Context, path string, args ...string) (string, error) {
		return m.run(ctx, path, args...)
	}}
}

func (m *Manager) sessionTmux(ctx context.Context, path string, session storage.CodexSession, args ...string) (string, error) {
	return m.backend().Command(ctx, path, runtimeTarget(session), args...)
}

// Session combines a conversation and exact run with service-derived actions.
type Session struct {
	storage.CodexSession
	AttachCommand string       `json:"attachCommand"`
	Continuation  Continuation `json:"continuation"`
}

func (m *Manager) describe(session storage.CodexSession) Session {
	return Session{CodexSession: session, AttachCommand: runtimeTarget(session).AttachCommand(), Continuation: m.continuation(session)}
}

func final(status string) bool { return status == "ended" || status == "lost" || status == "failed" }

func startupPending(session storage.CodexSession) bool {
	created, err := time.Parse(time.RFC3339Nano, session.CreatedAt)
	return session.Status == "starting" && err == nil && time.Since(created) < 30*time.Second
}

func missingRuntime(output string) bool {
	return strings.Contains(output, "can't find") || strings.Contains(output, "no server running") || strings.Contains(output, "No such file or directory")
}

// Reconcile only serializes this run. Unknown runtime errors never imply loss.
func (m *Manager) reconcile(ctx context.Context, session storage.CodexSession) (storage.CodexSession, error) {
	release, err := m.runGates.acquire(ctx, session.RunID)
	if err != nil {
		return session, err
	}
	defer release()
	// A caller may have waited behind End or another reconciliation. Never
	// apply the stale status read before acquiring this run's gate.
	session, err = m.store.CodexRun(ctx, session.ID, session.RunID)
	if err != nil {
		return session, err
	}
	session.Connections = m.connectionCount(session.RunID)
	current, runnerStatus, stateErr := m.syncRunner(ctx, session)
	if stateErr != nil {
		return session, stateErr
	}
	session = current
	session.Connections = m.connectionCount(session.RunID)
	if final(session.Status) {
		return session, nil
	}
	path := executable("tmux")
	if path == "" {
		return session, failure(Unavailable, "tmux_unavailable", "Install tmux to inspect or reconnect this session.")
	}
	output, err := m.sessionTmux(ctx, path, session, "list-panes", "-t", target(session)+":0", "-F", "#{session_name}\t#{pane_index}\t#{pane_dead}:#{pane_dead_status}")
	if err != nil {
		if missingRuntime(output) {
			// Another service may observe the durable reservation before its
			// bounded launch creates tmux. Do not abandon an in-flight start.
			if startupPending(session) {
				return session, nil
			}
			status, reason := "lost", "The persistent tmux run no longer exists. Continue its saved conversation when available."
			if session.Status == "stopping" {
				if err := m.awaitRunnerCleanup(ctx, session.ID); err != nil {
					return session, err
				}
				status, reason = "ended", "Session ended."
			}
			return m.store.UpdateCodexRun(ctx, session.RunID, status, reason)
		}
		return session, failure(Unavailable, "session_unavailable", "The tmux session could not be inspected. Check tmux on the host and try again.")
	}
	fields := strings.Split(output, "\t")
	if len(fields) != 3 || fields[0] != session.TmuxSession || fields[1] != "0" {
		return session, failure(Unavailable, "session_unavailable", "The tmux session identity could not be verified. Try again.")
	}
	output = fields[2]
	if session.Status == "stopping" {
		if _, err := m.sessionTmux(ctx, path, session, "kill-session", "-t", target(session)); err != nil {
			return session, failure(Unavailable, "session_unavailable", "The interrupted end operation could not be completed. Try again.")
		}
		if err := m.awaitRunnerCleanup(ctx, session.ID); err != nil {
			return session, err
		}
		return m.store.UpdateCodexRun(ctx, session.RunID, "ended", "Session ended.")
	}
	if strings.HasPrefix(output, "1:") {
		exit, signal := strings.TrimPrefix(output, "1:"), ""
		if !confirmedExitOutcome(exit, signal) {
			// tmux closes the pane PTY before waitpid necessarily publishes
			// its outcome. An empty status alone is not evidence of failure.
			for attempt := 0; attempt < 5; attempt++ {
				detail, inspectErr := m.sessionTmux(ctx, path, session, "display-message", "-p", "-t", runtimeTarget(session).PaneTarget(), "#{pane_dead_status}:#{pane_dead_signal}")
				if inspectErr != nil {
					return session, failure(Unavailable, "session_unavailable", "The tmux exit status could not be inspected. Try again.")
				}
				if status, cause, ok := strings.Cut(detail, ":"); ok && confirmedExitOutcome(status, cause) {
					exit, signal = status, cause
					break
				}
				if attempt < 4 {
					select {
					case <-ctx.Done():
						return session, ctx.Err()
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
			if !confirmedExitOutcome(exit, signal) {
				// Older supported tmux versions do not expose signal status.
				// Keep uncertainty explicit and leave exact-run End available.
				return session, failure(Unavailable, "session_exit_unconfirmed", "The terminal closed, but tmux has not confirmed its exit status. Try again or choose End Session.")
			}
		}
		status, reason := "ended", "Codex exited."
		if exit != "0" || signal != "" {
			status, reason = "failed", "Codex exited unexpectedly. Check the host CLI before continuing its saved conversation."
			if exit != "" {
				reason = "Codex exited with status " + exit + ". Check the host CLI before continuing its saved conversation."
			}
		}
		// Do not finalize metadata until the dead pane is removed. If cleanup
		// fails, the known live record remains visible and End Session can retry.
		if _, err := m.sessionTmux(ctx, path, session, "kill-session", "-t", target(session)); err != nil {
			return session, failure(Unavailable, "session_cleanup_failed", reason+" The terminal could not be cleaned up. Choose End Session to retry.")
		}
		return m.store.UpdateCodexRun(ctx, session.RunID, status, reason)
	}
	if runnerStatus == runner.StatusFailed {
		if _, err := m.sessionTmux(ctx, path, session, "kill-session", "-t", target(session)); err != nil {
			return session, failure(Unavailable, "session_cleanup_failed", "The Codex runner failed and cleanup could not be completed. Choose End Session to retry.")
		}
		return m.store.UpdateCodexRun(ctx, session.RunID, "failed", "Codex could not continue the verified conversation. Check its saved history and the host CLI; no replacement conversation was started.")
	}
	if session.Status == "starting" {
		ready, err := m.sessionTmux(ctx, path, session, "show-option", "-qv", "-t", session.TmuxSession, tmux.ReadyOption)
		if err != nil || ready != session.RunID {
			if startupPending(session) && session.Reason == "" {
				return session, nil
			}
			return session, failure(Unavailable, "startup_unconfirmed", "Session setup did not finish. Choose End Session, then start a new session.")
		}
		if runnerStatus != runner.StatusReady {
			return session, nil
		}
		return m.store.StartCodexRun(ctx, session.RunID)
	}
	return session, nil
}

func confirmedExitOutcome(status, signal string) bool {
	if status != "" {
		code, err := strconv.Atoi(status)
		return signal == "" && err == nil && code >= 0 && code <= 255 && strconv.Itoa(code) == status
	}
	if signal == "" {
		return false
	}
	// tmux reports a signal number on Linux and a signal name on BSD.
	if number, err := strconv.Atoi(signal); err == nil {
		maximum := 64 // Linux includes realtime signals through SIGRTMAX.
		if runtime.GOOS == "darwin" {
			maximum = 31
		}
		return number > 0 && number <= maximum && strconv.Itoa(number) == signal
	}
	// A syntactically alphabetic placeholder is still an unknown result. These
	// are the conventional signal names on the supported Linux/macOS hosts.
	switch strings.TrimPrefix(strings.ToUpper(signal), "SIG") {
	case "HUP", "INT", "QUIT", "ILL", "TRAP", "ABRT", "IOT", "EMT", "FPE", "KILL", "BUS", "SEGV", "SYS", "PIPE", "ALRM", "TERM", "URG", "STOP", "TSTP", "CONT", "CHLD", "TTIN", "TTOU", "IO", "POLL", "XCPU", "XFSZ", "VTALRM", "PROF", "WINCH", "INFO", "USR1", "USR2", "PWR", "STKFLT":
		return true
	}
	return false
}

func supportedTmux(version string) bool {
	fields := strings.Fields(version)
	if len(fields) != 2 || fields[0] != "tmux" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(fields[1], "next-"), ".")
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minorText := strings.TrimRightFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	minor, err := strconv.Atoi(minorText)
	return err == nil && (major > 3 || major == 3 && minor >= 1)
}

var codexVersionPattern = regexp.MustCompile(`^codex-cli ([0-9]+)\.([0-9]+)\.([0-9]+)((?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

func supportedCodex(version string) bool {
	parts := codexVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if parts == nil {
		return false
	}
	major, majorErr := strconv.Atoi(parts[1])
	minor, minorErr := strconv.Atoi(parts[2])
	patch, patchErr := strconv.Atoi(parts[3])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return false
	}
	return major > 0 || minor > 161 || minor == 161 && (patch > 0 || !strings.HasPrefix(parts[4], "-"))
}
