package codex

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func (m *Manager) notificationTarget(session storage.CodexSession, event runner.Activity) notificationevent.Target {
	target := notificationevent.Target{
		Kind: "codex", StoreID: m.databaseID, SessionID: session.ID, RunID: session.RunID,
		ThreadID: event.ThreadID, TurnID: event.TurnID, EventID: event.EventID,
	}
	if session.VMProject != nil {
		target.HostID = session.VMProject.Project.HostID
		target.ProjectID = session.VMProject.Project.ID
	}
	return target
}

// NotificationSources exposes bounded, content-free producer journals for all
// exact recorded runs. It never starts, resumes or submits work to a provider.
func (m *Manager) NotificationSources(ctx context.Context, cursors map[string]uint64) ([]notificationevent.Batch, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	sessions, err := m.store.CodexRuns(ctx)
	if err != nil {
		return nil, failure(Unavailable, "notification_activity_unavailable", "Codex notification activity could not be read. Check the AXV data directory and retry.")
	}
	result := []notificationevent.Batch{{SourceID: "codex-store", StoreID: m.databaseID}}
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch := notificationevent.Batch{SourceID: "codex:" + session.RunID, StoreID: m.databaseID}
		if session.ConversationID == "" {
			// The independent runner can become ready while the desktop is
			// stopped, before a normal UI read binds its verified identity.
			release, err := m.runGates.acquire(ctx, session.RunID)
			if err != nil {
				return nil, err
			}
			session, _, err = m.syncRunner(ctx, session)
			release()
			if err != nil {
				batch.Unavailable = true
				result = append(result, batch)
				continue
			}
		}
		if session.ConversationID == "" {
			result = append(result, batch)
			continue
		}
		manifest := filepath.Join(m.runDirectory(session.RunID), "manifest.json")
		// The producer publishes its journal first. Reading the snapshot
		// before that journal avoids mistaking an in-flight transition for
		// a missing journal tail.
		snapshot, snapshotErr := runner.ReadActivity(manifest, session.RunID, session.ConversationID)
		journal, err := runner.ReadActivityEvents(manifest, session.RunID, session.ConversationID, cursors[batch.SourceID])
		batch.First, batch.Next, batch.Gap, batch.Unavailable = journal.First, journal.Next, journal.Gap, journal.Unavailable
		if err != nil {
			batch.Unavailable = true
		} else {
			for _, event := range journal.Events {
				batch.Events = append(batch.Events, notificationevent.Event{
					ID: event.EventID, Sequence: event.Sequence, ObservedAt: event.UpdatedAt,
					State: event.State, Target: m.notificationTarget(session, event),
				})
			}
			// If a filesystem also refused the failure marker, a newer safe
			// snapshot still exposes the journal's unrecorded tail.
			if snapshotErr == nil && snapshot.Sequence > journal.Next {
				batch.Gap, batch.Unavailable = true, true
			}
			if !batch.Unavailable && !completedActivity(journal.Latest.State) {
				// A matching retained snapshot cannot prove that an exited
				// runner published every subsequent observation. Reconcile
				// only nonterminal tails, including fully consumed ones.
				current, inspectErr := m.visibleSession(ctx, session)
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if inspectErr != nil || current.Status != "running" || current.Reason != "" {
					// A completion can commit between the first read and
					// observing process exit. Confirm the retained tail before
					// declaring that a provider outcome may be missing.
					confirmed, confirmErr := runner.ReadActivityEvents(manifest, session.RunID, session.ConversationID, cursors[batch.SourceID])
					if confirmErr == nil && !confirmed.Unavailable && completedActivity(confirmed.Latest.State) {
						batch.First, batch.Next, batch.Gap, batch.Unavailable = confirmed.First, confirmed.Next, confirmed.Gap, confirmed.Unavailable
						batch.Events = nil
						for _, event := range confirmed.Events {
							batch.Events = append(batch.Events, notificationevent.Event{
								ID: event.EventID, Sequence: event.Sequence, ObservedAt: event.UpdatedAt,
								State: event.State, Target: m.notificationTarget(session, event),
							})
						}
					} else {
						batch.Gap, batch.Unavailable = true, true
					}
				}
			}
		}
		result = append(result, batch)
	}
	return result, nil
}

// ResolveNotification verifies the original stored run and event. An old
// target can never be substituted with a conversation's newest run or turn.
func (m *Manager) ResolveNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return notificationevent.Resolution{}, err
	}
	defer done()
	result := notificationevent.Resolution{Target: target}
	if target.Kind != "codex" || target.StoreID != m.databaseID || target.SessionID == "" || target.RunID == "" ||
		!conversationIDPattern.MatchString(target.ThreadID) || !activityEventPattern.MatchString(target.EventID) {
		return result, nil
	}
	release, err := m.runGates.acquire(ctx, target.RunID)
	if err != nil {
		return result, err
	}
	defer release()
	session, err := m.store.CodexRun(ctx, target.SessionID, target.RunID)
	if err != nil {
		var missing *storage.Error
		if errors.As(err, &missing) && missing.Code == "not_found" {
			return result, nil
		}
		return result, failure(Unavailable, "notification_activity_unavailable", "The original Codex notification target could not be verified. Refresh and retry.")
	}
	if session.ConversationID != target.ThreadID {
		return result, nil
	}
	manifest := filepath.Join(m.runDirectory(session.RunID), "manifest.json")
	journal, err := runner.ReadActivityEvents(manifest, session.RunID, session.ConversationID, 0)
	if err != nil {
		return result, nil
	}
	for _, event := range journal.Events {
		if event.EventID != target.EventID || m.notificationTarget(session, event) != target {
			continue
		}
		result.Available, result.State = true, event.State
		if completedActivity(event.State) {
			result.Current = true
			return result, nil
		}
		if journal.Unavailable {
			result.Available = false
			return result, nil
		}
		if journal.Next != event.Sequence {
			return result, nil
		}
		// Stored running metadata can outlive the independent process.
		// Pending requests need the original run's current runtime proof.
		session, err = m.visibleSessionLocked(ctx, session)
		if err != nil || session.Status != "running" || session.Reason != "" {
			result.Available = false
			return result, ctx.Err()
		}
		current := m.readActivity(session)
		if current.State == runner.ActivityUnknown {
			result.Available = false
			return result, nil
		}
		result.Current = current.EventID == event.EventID && current.State == event.State &&
			(event.State == runner.ActivityWaitingApproval || event.State == runner.ActivityWaitingInput)
		return result, nil
	}
	// Retention is a visible inability to verify the original historical
	// event, never permission to redirect the notification to fresh work.
	return result, nil
}
