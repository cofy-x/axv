//go:build darwin || linux

package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestActivityJournalRetainsTransitionsAcrossDesktopDowntime(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", map[string]any{
		"threadId": testThreadID, "turnId": "turn-a", "command": "private command", "prompt": "private prompt",
	}, "private request id"))
	observer.fromClient(wireMessage{ID: json.RawMessage(`"private request id"`), Result: json.RawMessage(`{"decision":"accept"}`)})
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-b", "inProgress"))
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-b", "failed"))
	observer = nil
	journal, err := ReadActivityEvents(manifest, "run", testThreadID, 0)
	if err != nil || journal.Gap || journal.Unavailable || journal.First != 1 || journal.Next != 8 {
		t.Fatalf("detached history = %+v, %v", journal, err)
	}
	var states []string
	for _, event := range journal.Events {
		states = append(states, event.State)
	}
	if !slices.Equal(states, []string{ActivityUnknown, ActivityIdle, ActivityWorking, ActivityWaitingApproval, ActivityWorking, ActivityCompleted, ActivityWorking, ActivityFailed}) {
		t.Fatalf("lost intermediate transition: %v", states)
	}
	replay, err := ReadActivityEvents(manifest, "run", testThreadID, 5)
	if err != nil || replay.Gap || len(replay.Events) != 3 || replay.Events[0].TurnID != "turn-a" || replay.Events[2].TurnID != "turn-b" {
		t.Fatalf("checkpoint replay = %+v, %v", replay, err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), activityEventsFile))
	if err != nil || strings.Contains(string(data), "private") || strings.Contains(string(data), "decision") {
		t.Fatalf("provider content entered journal: %q, %v", data, err)
	}
}

func TestActivityJournalRetentionAndInternalSequenceGapsAreVisible(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	for index := 0; index < maxActivityEvents+3; index++ {
		observer.advance(ActivityWorking, fmt.Sprintf("turn-%d", index), false)
	}
	journal, err := ReadActivityEvents(manifest, "run", testThreadID, 0)
	if err != nil || !journal.Gap || journal.Unavailable || len(journal.Events) != maxActivityEvents || journal.First != journal.Next-maxActivityEvents+1 {
		t.Fatalf("retained history = %+v, %v", journal, err)
	}
	checkpoint := journal.Next
	observer.journal = func(string, Activity) error { return errors.New("fixture journal failure") }
	observer.advance(ActivityWaitingInput, "turn-last", false)
	if snapshot := activityAt(t, manifest, ActivityWaitingInput); snapshot.Sequence != checkpoint+1 {
		t.Fatal("journal failure changed the independent activity snapshot")
	}
	failed, err := ReadActivityEvents(manifest, "run", testThreadID, checkpoint)
	if err != nil || !failed.Gap || !failed.Unavailable || failed.Next != checkpoint || len(failed.Events) != 0 {
		t.Fatalf("publication failure hidden: %+v, %v", failed, err)
	}
	observer.journal = appendActivityEvent
	observer.advance(ActivityCompleted, "turn-last", false)
	recovered, err := ReadActivityEvents(manifest, "run", testThreadID, checkpoint)
	if err != nil || !recovered.Gap || recovered.Unavailable || len(recovered.Events) != 1 || recovered.Events[0].Sequence != checkpoint+2 {
		t.Fatalf("recovery hid lost transition: %+v, %v", recovered, err)
	}
	after, err := ReadActivityEvents(manifest, "run", testThreadID, recovered.Next)
	if err != nil || after.Gap || after.Unavailable || len(after.Events) != 0 {
		t.Fatalf("confirmed cursor repeated a historical gap: %+v, %v", after, err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(manifest), activityEventsFile))
	if err != nil || info.Size() > maxActivityEventsBytes {
		t.Fatalf("journal exceeded retention bound: %v, %v", info, err)
	}
}

func TestActivityJournalSameEventRetriesWithoutDuplicateOrSnapshotChange(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.journal = func(string, Activity) error { return errors.New("fixture journal failure") }
	observer.record(ActivityWorking, "turn-a")
	before := activityAt(t, manifest, ActivityWorking)
	if !observer.journalDirty || observer.dirty {
		t.Fatal("journal failure changed snapshot publication status")
	}
	observer.journal = appendActivityEvent
	observer.record(ActivityWorking, "turn-a")
	journal, err := ReadActivityEvents(manifest, "run", testThreadID, 0)
	if err != nil || journal.Gap || journal.Unavailable || journal.Next != before.Sequence || len(journal.Events) != 3 || observer.journalDirty {
		t.Fatalf("same-event recovery = %+v, %v", journal, err)
	}
	if after := activityAt(t, manifest, ActivityWorking); after != before {
		t.Fatal("journal retry changed the existing snapshot semantics")
	}
}

func TestActivityJournalRejectsUnsafeOrDamagedHistoryWithoutReplacement(t *testing.T) {
	for _, kind := range []string{"run", "thread", "unknown", "duplicate", "case-duplicate", "escaped-duplicate", "nested-duplicate", "nested-case-duplicate", "nested-escaped-duplicate", "unicode-case-duplicate", "escaped-unicode-case-duplicate", "trailing", "sequence", "first", "public", "symlink", "hardlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			observer, manifest := activityTestObserver(t)
			path := filepath.Join(filepath.Dir(manifest), activityEventsFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "run":
				data = []byte(strings.ReplaceAll(string(data), `"runId":"run"`, `"runId":"other"`))
			case "thread":
				data = []byte(strings.ReplaceAll(string(data), testThreadID, otherThreadID))
			case "unknown":
				data = append([]byte(`{"unknown":true,`), data[1:]...)
			case "duplicate":
				data = append([]byte(`{"version":1,`), data[1:]...)
			case "case-duplicate":
				data = append([]byte(`{"VERSION":1,`), data[1:]...)
			case "escaped-duplicate":
				data = append([]byte(`{"\u0056ERSION":1,`), data[1:]...)
			case "nested-duplicate":
				data = []byte(strings.Replace(string(data), `"state":"unknown"`, `"state":"unknown","state":"unknown"`, 1))
			case "nested-case-duplicate":
				data = []byte(strings.Replace(string(data), `"state":"unknown"`, `"state":"unknown","turnId":"turn-a","STATE":"completed"`, 1))
			case "nested-escaped-duplicate":
				data = []byte(strings.Replace(string(data), `"state":"unknown"`, `"state":"unknown","turnId":"turn-a","\u0053TATE":"completed"`, 1))
			case "unicode-case-duplicate":
				data = []byte(strings.Replace(string(data), `"state":"unknown"`, `"state":"unknown","turnId":"turn-a","ſtate":"completed"`, 1))
			case "escaped-unicode-case-duplicate":
				data = []byte(strings.Replace(string(data), `"state":"unknown"`, `"state":"unknown","turnId":"turn-a","\u017ftate":"completed"`, 1))
			case "trailing":
				data = append(data, []byte(`{}`)...)
			case "sequence":
				data = []byte(strings.Replace(string(data), `"sequence":2`, `"sequence":1`, 1))
			case "first":
				data = []byte(strings.Replace(string(data), `"first":1`, `"first":2`, 1))
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink", "directory":
				original := path + ".original"
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(original, path)
				} else if kind == "hardlink" {
					err = os.Link(original, path)
				} else {
					err = os.Mkdir(path, 0o700)
				}
				if err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatal("unhandled fixture")
			}
			if kind != "public" && kind != "symlink" && kind != "hardlink" && kind != "directory" {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if journal, err := ReadActivityEvents(manifest, "run", testThreadID, 0); !errors.Is(err, ErrActivityUnavailable) || len(journal.Events) != 0 {
				t.Fatalf("unsafe history accepted: %+v, %v", journal, err)
			}
			observer.record(ActivityWorking, "next-turn")
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unsafe history was replaced: %v", err)
			}
			activityAt(t, manifest, ActivityWorking)
		})
	}
}
