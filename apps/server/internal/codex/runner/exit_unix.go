//go:build darwin || linux

package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxExitBytes = 1024

func exitDirectory(directory string) (*os.Root, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsRune(directory, '\x00') {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !ownershipPrivate(info) {
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) || !ownershipPrivate(opened) {
		_ = root.Close()
		return nil, ErrUnavailable
	}
	return root, nil
}

// RecordExit publishes a complete, private proof once, without replacing any
// existing outcome. Call only after children are reaped and ownership released.
func RecordExit(manifestPath string, code int) error {
	if code < 0 || code > 255 {
		return ErrUnavailable
	}
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || !ownershipPrivate(info) {
		return ErrUnavailable
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return ErrUnavailable
	}
	current, err := os.Lstat(manifestPath)
	if err != nil || !os.SameFile(info, current) || !ownershipPrivate(current) {
		return ErrUnavailable
	}
	root, err := exitDirectory(filepath.Dir(manifestPath))
	if err != nil {
		return ErrUnavailable
	}
	defer root.Close()
	data, err := json.Marshal(Exit{RunID: manifest.RunID, ExitCode: code})
	if err != nil || len(data)+1 > maxExitBytes {
		return ErrUnavailable
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return ErrUnavailable
	}
	temporary := ".exit-" + hex.EncodeToString(token[:])
	file, err := root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrUnavailable
	}
	defer root.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return ErrUnavailable
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return ErrUnavailable
	}
	if err := file.Sync(); err != nil {
		return ErrUnavailable
	}
	completed, err := file.Stat()
	if err != nil || file.Close() != nil {
		return ErrUnavailable
	}
	// A link atomically publishes only when the final name is absent. Unlike
	// rename, it cannot overwrite a malformed or already established proof.
	if err := root.Link(temporary, "exit.json"); err != nil {
		return ErrUnavailable
	}
	published := false
	defer func() {
		if !published {
			if current, err := root.Lstat("exit.json"); err == nil && os.SameFile(completed, current) {
				_ = root.Remove("exit.json")
			}
		}
	}()
	if err := root.Remove(temporary); err != nil {
		return ErrUnavailable
	}
	directory, err := root.Open(".")
	if err != nil {
		return ErrUnavailable
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return ErrUnavailable
	}
	published = true
	return nil
}

// ReadExit returns no proof for a missing file. Existing damage, unsafe paths
// and another run's outcome remain unavailability rather than an exit verdict.
func ReadExit(directory, runID string) (*Exit, error) {
	if runID == "" {
		return nil, ErrUnavailable
	}
	root, err := exitDirectory(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	defer root.Close()
	current, err := root.Lstat("exit.json")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !current.Mode().IsRegular() || !ownershipPrivate(current) || current.Size() > maxExitBytes {
		return nil, ErrUnavailable
	}
	file, err := root.OpenFile("exit.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(current, opened) ||
		!ownershipPrivate(opened) || opened.Size() > maxExitBytes {
		return nil, ErrUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxExitBytes))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrUnavailable
	}
	var outcome Exit
	var code *int
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return nil, ErrUnavailable
		}
		seen[key] = true
		switch key {
		case "runId":
			err = decoder.Decode(&outcome.RunID)
		case "exitCode":
			err = decoder.Decode(&code)
		default:
			return nil, ErrUnavailable
		}
		if err != nil {
			return nil, ErrUnavailable
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF || outcome.RunID != runID || code == nil || *code < 0 || *code > 255 {
		return nil, ErrUnavailable
	}
	outcome.ExitCode = *code
	return &outcome, nil
}
