//go:build darwin || linux

package runner

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
)

const activityUnavailableFile = "activity-unavailable"

func writeActivity(path string, activity Activity) error {
	if !validActivity(activity, activity.RunID, activity.ThreadID) {
		return ErrActivityUnavailable
	}
	data, err := json.Marshal(activity)
	if err != nil || len(data)+1 > maxActivityBytes {
		return ErrActivityUnavailable
	}
	if atomicfile.Write(path, append(data, '\n'), 0o600) != nil {
		return ErrActivityUnavailable
	}
	marker := filepath.Join(filepath.Dir(path), activityUnavailableFile)
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return ErrActivityUnavailable
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrActivityUnavailable
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrActivityUnavailable
	}
	return nil
}

func markActivityUnavailable(path string) {
	// If the filesystem cannot publish a marker, removing the old observation
	// still prevents a stale snapshot from masquerading as fresh activity.
	_ = atomicfile.Write(filepath.Join(filepath.Dir(path), activityUnavailableFile), []byte("activity unavailable\n"), 0o600)
	_ = os.Remove(path)
}

// ReadActivity reads only the bounded private observation beside the recorded
// manifest. Missing snapshots return os.ErrNotExist, allowing older live runs
// to remain unknown without changing their readiness or process outcome.
func ReadActivity(manifestPath, runID, threadID string) (Activity, error) {
	var activity Activity
	if !filepath.IsAbs(manifestPath) || filepath.Clean(manifestPath) != manifestPath ||
		!activityIDPattern.MatchString(runID) || !threadIDPattern.MatchString(threadID) {
		return activity, ErrActivityUnavailable
	}
	root, err := exitDirectory(filepath.Dir(manifestPath))
	if err != nil {
		if os.IsNotExist(err) {
			return activity, os.ErrNotExist
		}
		return activity, ErrActivityUnavailable
	}
	defer root.Close()
	if _, err := root.Lstat(activityUnavailableFile); !os.IsNotExist(err) {
		return activity, ErrActivityUnavailable
	}
	current, err := root.Lstat("activity.json")
	if os.IsNotExist(err) {
		return activity, os.ErrNotExist
	}
	if err != nil || !current.Mode().IsRegular() || !ownershipPrivate(current) || current.Size() > maxActivityBytes {
		return activity, ErrActivityUnavailable
	}
	file, err := root.OpenFile("activity.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return activity, ErrActivityUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !ownershipPrivate(opened) ||
		!os.SameFile(current, opened) || opened.Size() > maxActivityBytes {
		return activity, ErrActivityUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxActivityBytes))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return activity, ErrActivityUnavailable
	}
	seen := make(map[string]bool, 7)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return Activity{}, ErrActivityUnavailable
		}
		seen[key] = true
		switch key {
		case "runId":
			err = decoder.Decode(&activity.RunID)
		case "threadId":
			err = decoder.Decode(&activity.ThreadID)
		case "state":
			err = decoder.Decode(&activity.State)
		case "eventId":
			err = decoder.Decode(&activity.EventID)
		case "sequence":
			err = decoder.Decode(&activity.Sequence)
		case "turnId":
			err = decoder.Decode(&activity.TurnID)
		case "updatedAt":
			err = decoder.Decode(&activity.UpdatedAt)
		default:
			return Activity{}, ErrActivityUnavailable
		}
		if err != nil {
			return Activity{}, ErrActivityUnavailable
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return Activity{}, ErrActivityUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF || !validActivity(activity, runID, threadID) {
		return Activity{}, ErrActivityUnavailable
	}
	// The writer may have discovered a failed publication while this read was
	// in flight. Fail closed when its marker is now visible.
	if _, err := root.Lstat(activityUnavailableFile); !os.IsNotExist(err) {
		return Activity{}, ErrActivityUnavailable
	}
	return activity, nil
}
