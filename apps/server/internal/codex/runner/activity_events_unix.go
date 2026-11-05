//go:build darwin || linux

package runner

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// uniqueActivityJSON rejects duplicate fields, including nested Activity
// objects. Typed decoding separately rejects unknown fields and invalid values.
func uniqueActivityJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrActivityUnavailable
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrActivityUnavailable
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			for _, character := range name {
				if character > 127 {
					return ErrActivityUnavailable
				}
			}
			name = strings.ToLower(name)
			if err != nil || !ok || seen[name] {
				return ErrActivityUnavailable
			}
			seen[name] = true
			if uniqueActivityJSON(decoder, depth+1) != nil {
				return ErrActivityUnavailable
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrActivityUnavailable
		}
	case '[':
		for decoder.More() {
			if uniqueActivityJSON(decoder, depth+1) != nil {
				return ErrActivityUnavailable
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrActivityUnavailable
		}
	default:
		return ErrActivityUnavailable
	}
	return nil
}

func readActivityEventJournal(root *os.Root, runID, threadID string) (activityEventJournal, error) {
	var journal activityEventJournal
	entry, err := root.Lstat(activityEventsFile)
	if os.IsNotExist(err) {
		return journal, os.ErrNotExist
	}
	if err != nil || !entry.Mode().IsRegular() || !ownershipPrivate(entry) || entry.Size() > maxActivityEventsBytes {
		return journal, ErrActivityUnavailable
	}
	file, err := root.OpenFile(activityEventsFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return journal, ErrActivityUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !ownershipPrivate(opened) || !os.SameFile(entry, opened) || opened.Size() > maxActivityEventsBytes {
		return journal, ErrActivityUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxActivityEventsBytes+1))
	if err != nil || len(data) > maxActivityEventsBytes {
		return journal, ErrActivityUnavailable
	}
	unique := json.NewDecoder(bytes.NewReader(data))
	if uniqueActivityJSON(unique, 0) != nil || unique.Decode(new(any)) != io.EOF {
		return journal, ErrActivityUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&journal) != nil || decoder.Decode(new(any)) != io.EOF ||
		journal.Version != 1 || journal.RunID != runID || journal.ThreadID != threadID ||
		len(journal.Events) == 0 || len(journal.Events) > maxActivityEvents {
		return activityEventJournal{}, ErrActivityUnavailable
	}
	var previous uint64
	seen := make(map[string]bool, len(journal.Events))
	for _, event := range journal.Events {
		if !validActivity(event, runID, threadID) || event.Sequence <= previous || seen[event.EventID] {
			return activityEventJournal{}, ErrActivityUnavailable
		}
		previous = event.Sequence
		seen[event.EventID] = true
	}
	if journal.First != journal.Events[0].Sequence || journal.Last != previous {
		return activityEventJournal{}, ErrActivityUnavailable
	}
	return journal, nil
}

func publishActivityEvents(root *os.Root, name string, data []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrActivityUnavailable
	}
	temporary := ".activity-events-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrActivityUnavailable
	}
	defer root.Remove(temporary)
	defer file.Close()
	if file.Chmod(0o600) != nil {
		return ErrActivityUnavailable
	}
	if _, err := file.Write(data); err != nil || file.Sync() != nil || file.Close() != nil {
		return ErrActivityUnavailable
	}
	if root.Rename(temporary, name) != nil {
		return ErrActivityUnavailable
	}
	directory, err := root.Open(".")
	if err != nil {
		return ErrActivityUnavailable
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrActivityUnavailable
	}
	return nil
}

func markActivityEventsUnavailable(path string) {
	root, err := exitDirectory(filepath.Dir(path))
	if err != nil {
		return
	}
	defer root.Close()
	// Preserve the last journal; it can establish what was recorded before
	// the failure. A later append retains any skipped sequence as a gap.
	_ = publishActivityEvents(root, activityEventsUnavailableFile, []byte("activity events unavailable\n"))
}

func appendActivityEvent(path string, activity Activity) error {
	if !validActivity(activity, activity.RunID, activity.ThreadID) {
		return ErrActivityUnavailable
	}
	root, err := exitDirectory(filepath.Dir(path))
	if err != nil {
		return ErrActivityUnavailable
	}
	defer root.Close()
	journal, err := readActivityEventJournal(root, activity.RunID, activity.ThreadID)
	if os.IsNotExist(err) {
		journal = activityEventJournal{Version: 1, RunID: activity.RunID, ThreadID: activity.ThreadID}
	} else if err != nil {
		// Damage and unsafe replacement never initialize a fresh history.
		return ErrActivityUnavailable
	}
	if activity.Sequence <= journal.Last {
		if activity.Sequence != journal.Last || journal.Events[len(journal.Events)-1] != activity {
			return ErrActivityUnavailable
		}
	} else {
		journal.Events = append(journal.Events, activity)
		if len(journal.Events) > maxActivityEvents {
			journal.Events = journal.Events[len(journal.Events)-maxActivityEvents:]
		}
		journal.First, journal.Last = journal.Events[0].Sequence, activity.Sequence
	}
	data, err := json.Marshal(journal)
	if err != nil || len(data)+1 > maxActivityEventsBytes || publishActivityEvents(root, activityEventsFile, append(data, '\n')) != nil {
		return ErrActivityUnavailable
	}
	if err := root.Remove(activityEventsUnavailableFile); err != nil && !os.IsNotExist(err) {
		return ErrActivityUnavailable
	}
	directory, err := root.Open(".")
	if err != nil {
		return ErrActivityUnavailable
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrActivityUnavailable
	}
	return nil
}

// ReadActivityEvents reads retained metadata after a consumer's durable cursor.
// Gaps are explicit when retention or failed publications skipped a sequence.
func ReadActivityEvents(manifestPath, runID, threadID string, after uint64) (ActivityEvents, error) {
	var result ActivityEvents
	if !filepath.IsAbs(manifestPath) || filepath.Clean(manifestPath) != manifestPath ||
		!activityIDPattern.MatchString(runID) || !threadIDPattern.MatchString(threadID) || after > maxActivitySequence {
		return result, ErrActivityUnavailable
	}
	root, err := exitDirectory(filepath.Dir(manifestPath))
	if err != nil {
		if os.IsNotExist(err) {
			return result, os.ErrNotExist
		}
		return result, ErrActivityUnavailable
	}
	defer root.Close()
	journal, err := readActivityEventJournal(root, runID, threadID)
	if err != nil {
		if _, markerErr := root.Lstat(activityEventsUnavailableFile); !os.IsNotExist(markerErr) {
			return ActivityEvents{Gap: true, Unavailable: true}, ErrActivityUnavailable
		}
		return result, err
	}
	result.First, result.Next = journal.First, journal.Last
	result.Latest = journal.Events[len(journal.Events)-1]
	result.Gap = after < journal.First-1 || after > journal.Last
	previous := after
	for _, event := range journal.Events {
		if event.Sequence <= after {
			continue
		}
		if event.Sequence != previous+1 {
			result.Gap = true
		}
		result.Events = append(result.Events, event)
		previous = event.Sequence
	}
	if _, err := root.Lstat(activityEventsUnavailableFile); !os.IsNotExist(err) {
		result.Gap, result.Unavailable = true, true
	}
	return result, nil
}
