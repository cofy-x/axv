// Package runner keeps one official Codex conversation in one owned
// terminal runtime. Conversation identity comes from the official protocol.
package runner

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

const (
	ModeNew    = "new"
	ModeResume = "resume"

	StatusStarting = "starting"
	StatusReady    = "ready"
	StatusFailed   = "failed"
)

var (
	ErrHistoryUnavailable = errors.New("Codex conversation history is unavailable")
	ErrUnavailable        = errors.New("Codex history service is unavailable")
	ErrProtocol           = errors.New("Codex returned an incompatible conversation protocol")
	threadIDPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Manifest contains no credentials or transcript. The server creates one
// private manifest for each runtime attempt, before launching it in tmux.
type Manifest struct {
	SessionID     string `json:"sessionId"`
	RunID         string `json:"runId"`
	ProjectPath   string `json:"projectPath"`
	CodexPath     string `json:"codexPath"`
	HistoryHome   string `json:"historyHome"`
	SocketRoot    string `json:"socketRoot"`
	OwnershipPath string `json:"ownershipPath"`
	Mode          string `json:"mode"`
	ThreadID      string `json:"threadId,omitempty"`
	VMContextPath string `json:"vmContextPath,omitempty"`
	VMSkillPath   string `json:"vmSkillPath,omitempty"`
	vmWork        *vmwork.Context
}

// State publishes provider readiness separately from the final Exit proof.
// Ready means the exact conversation identity has been durably recorded.
type State struct {
	RunID          string `json:"runId"`
	ThreadID       string `json:"threadId,omitempty"`
	HistoryHome    string `json:"historyHome"`
	Status         string `json:"status"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ContextVersion int    `json:"contextVersion,omitempty"`
}

func readManifest(path string) (Manifest, error) {
	var manifest Manifest
	file, err := os.Open(path)
	if err != nil {
		return manifest, ErrUnavailable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0o077 != 0 {
		return manifest, ErrUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF {
		return manifest, ErrUnavailable
	}
	if manifest.RunID == "" || manifest.SessionID == "" ||
		!filepath.IsAbs(manifest.CodexPath) || !filepath.IsAbs(manifest.ProjectPath) || !filepath.IsAbs(manifest.HistoryHome) || ValidateSocketRoot(manifest.SocketRoot) != nil ||
		!filepath.IsAbs(manifest.OwnershipPath) || filepath.Clean(manifest.OwnershipPath) != manifest.OwnershipPath || strings.ContainsRune(manifest.OwnershipPath, '\x00') ||
		(manifest.Mode != ModeNew && manifest.Mode != ModeResume) ||
		(manifest.Mode == ModeNew && manifest.ThreadID != "") ||
		(manifest.Mode == ModeResume && !threadIDPattern.MatchString(manifest.ThreadID)) {
		return manifest, ErrUnavailable
	}
	if validateVMWorkPaths(manifest, filepath.Dir(path)) != nil {
		return manifest, ErrUnavailable
	}
	return manifest, nil
}

const socketDirectoryNameLength = 8

// ValidateSocketRoot reserves room for a short per-run directory and socket.
// Paths are explicit profile locations; there is no system temporary fallback.
func ValidateSocketRoot(root string) error {
	if !filepath.IsAbs(root) || strings.ContainsRune(root, '\x00') || filepath.Clean(root) != root {
		return errors.New("the Codex socket root must be an absolute, clean AXV profile path")
	}
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	if len(filepath.Join(root, strings.Repeat("x", socketDirectoryNameLength), "s")) > limit {
		return errors.New("the Codex socket path is too long; select a shorter AXV_HOME (AXV_DEV_HOME for make dev)")
	}
	return nil
}

func writeState(path string, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return ErrUnavailable
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return ErrUnavailable
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	if os.Rename(name, path) != nil {
		return ErrUnavailable
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrUnavailable
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}

func writeFailure(path string, fallback State, code string) {
	// A failure can race the bridge's first successful identity publication.
	// Preserve that durable identity even when the server has already exited.
	if data, err := os.ReadFile(path); err == nil {
		var current State
		if json.Unmarshal(data, &current) == nil && current.RunID == fallback.RunID &&
			current.HistoryHome == fallback.HistoryHome && threadIDPattern.MatchString(current.ThreadID) {
			fallback.ThreadID = current.ThreadID
			fallback.ContextVersion = current.ContextVersion
		}
	}
	fallback.Status, fallback.ErrorCode = StatusFailed, code
	_ = writeState(path, fallback)
}
