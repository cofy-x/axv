package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

var conversationIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (m *Manager) defaultRunnerCommand(manifest string) ([]string, error) {
	if m.settings.RunnerExecutable == "" {
		return nil, failure(Unavailable, "codex_runtime_unavailable", "The fixed AXV runner executable is unavailable. Restart AXV before starting a Codex session.")
	}
	return []string{m.settings.RunnerExecutable, "internal-codex-run", manifest}, nil
}

func verifyCodexHistory(ctx context.Context, cliPath, project, home string, env []string, threadID string) error {
	return runner.VerifyHistory(ctx, cliPath, project, home, env, threadID)
}

func historyHome() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(user, ".codex")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	// Codex may create its history directory after the reservation is saved.
	// Resolve the existing parent now so that creation cannot change the saved
	// identity merely by making a symlinked prefix (such as /tmp) resolvable.
	ancestor, suffix := home, ""
	for {
		canonical, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			info, err := os.Stat(canonical)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("Codex history parent is not a directory")
			}
			return filepath.Join(canonical, suffix), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// A dangling symlink is an existing, unresolved component. Do not
		// mistake it for a missing directory and persist a guessed location.
		if _, statErr := os.Lstat(ancestor); statErr == nil || !os.IsNotExist(statErr) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(ancestor), suffix)
		ancestor = parent
	}
}

func (m *Manager) runDirectory(runID string) string {
	return filepath.Join(m.settings.DataDir, "codex-runs", runID)
}

func (m *Manager) prepareRunner(info Info, session storage.CodexSession) ([]string, error) {
	dir := m.runDirectory(session.RunID)
	path := filepath.Join(dir, "manifest.json")
	command, err := m.runnerCommand(path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(m.settings.ProfileHome) {
		return nil, failure(Unavailable, "codex_runtime_unavailable", "The AXV profile directory is unavailable. Restart AXV before starting a Codex session.")
	}
	socketRoot := filepath.Join(m.settings.ProfileHome, "run", "codex")
	if err := runner.ValidateSocketRoot(socketRoot); err != nil {
		return nil, failure(Unavailable, "codex_runtime_unavailable", err.Error())
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	manifest := runner.Manifest{SessionID: session.ID, RunID: session.RunID, ProjectPath: session.ProjectPath, CodexPath: info.CodexPath, HistoryHome: session.HistoryHome, SocketRoot: socketRoot, Mode: session.RunMode()}
	if session.RunMode() == "resume" {
		manifest.ThreadID = session.ConversationID
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return command, nil
}

// State is metadata from the owned runner, never parsed terminal output. It is
// read before runtime reconciliation so an exited process can retain its identity.
func (m *Manager) syncRunner(ctx context.Context, session storage.CodexSession) (storage.CodexSession, string, error) {
	file, err := os.Open(filepath.Join(m.runDirectory(session.RunID), "state.json"))
	if os.IsNotExist(err) {
		return session, runner.StatusStarting, nil
	}
	if err != nil {
		return session, "", failure(Unavailable, "history_state_unavailable", "The Codex runner state could not be read. End this run or retry after checking the AXV data directory.")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0077 != 0 {
		return session, "", failure(Unavailable, "history_state_invalid", "The Codex runner state could not be verified.")
	}
	var state runner.State
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.RunID != session.RunID || state.HistoryHome != session.HistoryHome ||
		(state.Status != runner.StatusStarting && state.Status != runner.StatusReady && state.Status != runner.StatusFailed) ||
		(state.ThreadID != "" && !conversationIDPattern.MatchString(state.ThreadID)) || (state.Status == runner.StatusReady && state.ThreadID == "") {
		return session, "", failure(Unavailable, "history_state_invalid", "The Codex runner state could not be verified.")
	}
	if state.ThreadID != "" && (state.Status == runner.StatusReady || state.Status == runner.StatusFailed) {
		if session.ConversationID != state.ThreadID {
			session, err = m.store.BindCodexConversation(ctx, session.RunID, state.ThreadID, state.HistoryHome)
			if err != nil {
				return session, "", err
			}
		}
	}
	if state.Status == runner.StatusReady && session.Status == "starting" {
		// Runner readiness is durable proof that the official conversation was
		// initialized, even when End or process exit precedes the first poll.
		session, err = m.store.StartCodexRun(ctx, session.RunID)
		if err != nil {
			return session, "", err
		}
	}
	return session, state.Status, nil
}

type Continuation struct {
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	ActiveRunID string `json:"activeRunId,omitempty"`
}

func (m *Manager) continuation(session storage.CodexSession) Continuation {
	active, err := m.store.ActiveCodexRun(m.ctx, session.ID)
	if err == nil {
		return Continuation{State: "active", ActiveRunID: active.RunID}
	}
	var absent *storage.Error
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		return Continuation{State: "unavailable", Reason: "The current run could not be checked. Refresh and try again."}
	}
	if session.ConversationID == "" || session.HistoryHome == "" {
		return Continuation{State: "unavailable", Reason: "The official conversation identity was not recorded for this session."}
	}
	return Continuation{State: "available"}
}
