package codex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// Activity describes provider work independently of the process lifecycle.
// It contains no transcript, command, approval content or provider credentials.
type Activity struct {
	Observation    ActivityObservation `json:"observation"`
	State          string              `json:"state"`
	EventID        string              `json:"eventId,omitempty"`
	Sequence       uint64              `json:"sequence,omitempty"`
	TurnID         string              `json:"turnId,omitempty"`
	UpdatedAt      string              `json:"updatedAt,omitempty"`
	NeedsAttention bool                `json:"needsAttention"`
	Reason         string              `json:"reason,omitempty"`
}

// ActivityObservation orders service reads independently of provider events.
// The instance changes on service restart; revisions never compare across it.
type ActivityObservation struct {
	InstanceID string `json:"instanceId"`
	Revision   uint64 `json:"revision"`
}

type activityReview struct {
	RunID   string `json:"runId"`
	EventID string `json:"eventId"`
}

var activityEventPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func completedActivity(state string) bool {
	return state == runner.ActivityCompleted || state == runner.ActivityFailed || state == runner.ActivityInterrupted
}

func (m *Manager) activity(session storage.CodexSession) Activity {
	_, activity := m.activitySnapshot(session, false)
	return activity
}

func (m *Manager) activitySnapshot(session storage.CodexSession, observed bool) (storage.CodexSession, Activity) {
	// Serialize the bounded snapshot reads, so late responses cannot undo a
	// later review, uncertainty diagnostic or recovery of the same event.
	m.activityMu.Lock()
	defer m.activityMu.Unlock()
	var activity Activity
	if !observed && session.ID != "" && session.RunID != "" {
		// Ungated descriptions can retain a run from before End, startup or
		// another inspection. Refresh its persisted lifecycle under the same
		// observation lock, without replaying an old transient error.
		current, err := m.store.CodexRun(m.ctx, session.ID, session.RunID)
		if err != nil {
			activity = Activity{State: runner.ActivityUnknown, Reason: "The run's current state could not be verified. Refresh before relying on its activity."}
		} else {
			current.Connections = session.Connections
			session = current
		}
	}
	if activity.State == "" {
		activity = m.readActivity(session)
	}
	if m.activityRead == 1<<53-1 {
		m.activityEpoch, m.activityRead = rand.Text(), 0
	}
	m.activityRead++
	activity.Observation = ActivityObservation{InstanceID: m.activityEpoch, Revision: m.activityRead}
	return session, activity
}

func (m *Manager) readActivity(session storage.CodexSession) Activity {
	unknown := Activity{State: runner.ActivityUnknown}
	if session.ConversationID == "" {
		unknown.Reason = "Activity is available after Codex records its conversation."
		return unknown
	}
	snapshot, err := runner.ReadActivity(filepath.Join(m.runDirectory(session.RunID), "manifest.json"), session.RunID, session.ConversationID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			unknown.Reason = "This run has not reported activity. Its process status remains available."
		} else {
			unknown.Reason = "Activity could not be verified. Check the session and refresh; its process status is separate."
		}
		return unknown
	}
	activity := Activity{
		State: snapshot.State, EventID: snapshot.EventID, Sequence: snapshot.Sequence,
		TurnID: snapshot.TurnID, UpdatedAt: snapshot.UpdatedAt.Format(time.RFC3339Nano),
	}
	if activity.State == runner.ActivityUnknown {
		activity.Reason = "Codex could not confirm its current activity. Open this run and refresh."
		return activity
	}
	if !completedActivity(activity.State) && (session.Status != "running" || session.Reason != "") {
		activity.State = runner.ActivityUnknown
		activity.Reason = "The run is not confirmed running. Its last activity does not establish current work."
		return activity
	}
	activity.NeedsAttention = activity.State == runner.ActivityWaitingApproval || activity.State == runner.ActivityWaitingInput
	if activity.State != runner.ActivityFailed && activity.State != runner.ActivityInterrupted {
		return activity
	}
	review, err := m.readActivityReview(session.RunID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		activity.NeedsAttention = true
		activity.Reason = "The alert dismissal record could not be verified. It was not replaced; check the AXV data directory."
		return activity
	}
	activity.NeedsAttention = review.EventID != activity.EventID
	return activity
}

func (m *Manager) readActivityReview(runID string) (activityReview, error) {
	var review activityReview
	file, err := openActivityReview(m.runDirectory(runID))
	if err != nil {
		return review, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return review, runner.ErrActivityUnavailable
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return review, runner.ErrActivityUnavailable
		}
		seen[key] = true
		switch key {
		case "runId":
			err = decoder.Decode(&review.RunID)
		case "eventId":
			err = decoder.Decode(&review.EventID)
		default:
			return review, runner.ErrActivityUnavailable
		}
		if err != nil {
			return review, runner.ErrActivityUnavailable
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return review, runner.ErrActivityUnavailable
	}
	if decoder.InputOffset() > 4096 || decoder.Decode(new(any)) != io.EOF || review.RunID != runID || !activityEventPattern.MatchString(review.EventID) {
		return review, runner.ErrActivityUnavailable
	}
	return review, nil
}

// ReviewActivity dismisses one failed or interrupted turn alert, never an
// approval or another run's work. A later event remains actionable even if it
// arrives between validation and the atomic write of this event's record.
func (m *Manager) ReviewActivity(ctx context.Context, id, runID, eventID string) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	if !activityEventPattern.MatchString(eventID) {
		return Session{}, failure(Invalid, "invalid_activity_event", "Provide the current activity event identifier shown for this run.")
	}
	release, err := m.runGates.acquire(ctx, runID)
	if err != nil {
		return Session{}, err
	}
	defer release()
	session, err := m.store.CodexRun(ctx, id, runID)
	if err != nil {
		return Session{}, err
	}
	activity := m.activity(session)
	if activity.State == runner.ActivityUnknown {
		return Session{}, failure(Unavailable, "activity_unavailable", "The activity could not be verified. Refresh this run before dismissing its alert.")
	}
	if activity.EventID != eventID {
		return Session{}, failure(Conflict, "activity_changed", "This run has newer activity. Refresh before dismissing the current alert.")
	}
	if activity.State == runner.ActivityCompleted {
		return Session{}, failure(Conflict, "activity_not_actionable", "This turn completed successfully and has no alert to dismiss.")
	}
	if activity.State != runner.ActivityFailed && activity.State != runner.ActivityInterrupted {
		return Session{}, failure(Conflict, "activity_pending", "Open this run to answer pending requests in Codex. Only failed or interrupted turn alerts can be dismissed.")
	}
	if _, err := m.readActivityReview(runID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Session{}, failure(Unavailable, "activity_review_unavailable", "The existing alert dismissal record could not be verified. Check the AXV data directory; it was not replaced.")
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if err := jsonfile.Write(filepath.Join(m.runDirectory(runID), "activity-review.json"), activityReview{RunID: runID, EventID: eventID}, 0600); err != nil {
		return Session{}, failure(Unavailable, "activity_review_unavailable", "The alert dismissal could not be saved. Refresh and retry; the alert remains visible until its save is confirmed.")
	}
	return m.describe(session), nil
}
