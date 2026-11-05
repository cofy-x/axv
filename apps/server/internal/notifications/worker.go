package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	intake := time.NewTicker(10 * time.Second)
	defer intake.Stop()
	delivery := time.NewTicker(2 * time.Second)
	defer delivery.Stop()
	m.collect(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-intake.C:
			m.collect(ctx)
		case <-delivery.C:
			m.dispatch(ctx)
		}
	}
}

func (m *Manager) collect(ctx context.Context) {
	m.mu.Lock()
	if m.completion != nil {
		if m.save(*m.completion) != nil {
			m.mu.Unlock()
			return
		}
		m.completion = nil
	}
	cursors := map[string]uint64{}
	for key, c := range m.state.Checkpoints {
		cursors[key] = c.Sequence
	}
	m.mu.Unlock()
	var all []notificationevent.Batch
	failed := false
	for _, source := range m.options.Sources {
		request, cancel := context.WithTimeout(ctx, 15*time.Second)
		batches, err := source.Read(request, cursors)
		cancel()
		if err != nil {
			failed = true
		} else {
			all = append(all, batches...)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := clone(m.state)
	if !next.Intake.Gap {
		next.Intake.Error = ""
	}
	for _, batch := range all {
		m.ingest(&next, batch)
	}
	if failed {
		next.Intake.Error = "Work event intake is unavailable. Original checkpoints are preserved."
	}
	if !equal(next, m.state) || m.failure != "" {
		_ = m.save(next)
	}
}

func eventKind(e notificationevent.Event) string {
	if e.Target.Kind == "codex" {
		switch e.State {
		case "completed":
			return "agent_succeeded"
		case "waiting_approval", "waiting_input", "failed", "interrupted":
			return "agent_" + e.State
		}
	} else if e.Target.Kind == "task" {
		switch e.State {
		case "succeeded", "failed", "cancelled", "interrupted", "unknown":
			return "task_" + e.State
		}
	}
	return ""
}

func waiting(kind string) bool {
	return kind == "agent_waiting_approval" || kind == "agent_waiting_input"
}

func scope(t notificationevent.Target) string {
	if t.Kind == "codex" {
		return t.StoreID + ":" + t.RunID
	}
	return t.StoreID + ":" + t.AttemptID
}

func selected(types []string, kind string) bool {
	for _, t := range types {
		if t == kind {
			return true
		}
	}
	return false
}

func (m *Manager) ingest(s *diskState, b notificationevent.Batch) {
	if !identifier(b.SourceID, true, 256) || !identifier(b.StoreID, true, 128) || b.Next > 1<<53-1 || len(b.Events) > 2048 {
		s.Intake.Gap = true
		s.Intake.Error = "An event source returned invalid metadata."
		return
	}
	previous, exists := s.Checkpoints[b.SourceID]
	if !exists && strings.HasPrefix(b.SourceID, "compute:") {
		for key := range s.Checkpoints {
			if strings.HasPrefix(key, "compute:") && key != b.SourceID {
				s.Intake.Gap = true
				s.Intake.Error = "A VM host store was replaced. Old notifications retain their original targets."
			}
		}
	}
	if !exists && len(s.Checkpoints) >= maxSources {
		s.Intake.Gap = true
		s.Intake.Error = "Event source retention is full."
		return
	}
	if exists && previous.StoreID != b.StoreID {
		s.Intake.Gap = true
		s.Intake.Error = "A work store was replaced. Old notifications retain their original targets."
		previous = checkpoint{StoreID: b.StoreID}
	}
	if b.Gap || b.Unavailable {
		s.Intake.Gap = true
		s.Intake.Error = "Some work events could not be observed. No missing outcome was inferred."
	}
	if b.Next < previous.Sequence {
		s.Intake.Gap = true
		s.Intake.Error = "A work event stream moved backwards."
		return
	}
	last := previous.Sequence
	for _, e := range b.Events {
		if e.Sequence <= previous.Sequence {
			continue
		}
		if e.Sequence <= last || e.Sequence > b.Next || e.ID == "" || !validText(e.ID, 256) || e.ObservedAt.IsZero() || e.Target.StoreID != b.StoreID || !validTarget(e.Target) || e.Target.EventID != e.ID {
			s.Intake.Gap = true
			continue
		}
		last = e.Sequence
		// Any later owner observation obsoletes older waiting messages for that run.
		for i := range s.Events {
			old := &s.Events[i]
			if waiting(old.Kind) && scope(old.Target) == scope(e.Target) && old.Target.EventID != e.Target.EventID && (old.Status == "pending" || old.Status == "uncertain") {
				old.Status = "superseded"
				old.NextAttemptAt = nil
			}
		}
		kind := eventKind(e)
		if kind == "" || !s.Config.Enabled || e.ObservedAt.Before(s.EnabledAt) || !selected(s.Config.EventTypes, kind) {
			continue
		}
		if !m.room(s) {
			continue
		}
		digest := sha256.Sum256([]byte(b.StoreID + "\x00" + b.SourceID + "\x00" + e.ID))
		id := hex.EncodeToString(digest[:16])
		duplicate := false
		for _, existing := range s.Events {
			if existing.ID == id {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.Events = append(s.Events, Notification{ID: id, CreatedAt: e.ObservedAt.UTC(), Kind: kind, State: e.State, Status: "pending", Target: e.Target})
		}
	}
	// Checkpoint and accepted outbox records commit in the same atomic snapshot.
	s.Checkpoints[b.SourceID] = checkpoint{StoreID: b.StoreID, Sequence: b.Next}
}

func (m *Manager) dispatch(ctx context.Context) {
	m.mu.Lock()
	if !m.state.Config.Enabled || m.failure != "" || m.state.NextSendAt != nil && m.now().Before(*m.state.NextSendAt) {
		m.mu.Unlock()
		return
	}
	var event Notification
	for _, e := range m.state.Events {
		if (e.Status == "pending" || e.Status == "uncertain" && e.NextAttemptAt != nil) && e.Attempts < maxAttempts && (e.NextAttemptAt == nil || !m.now().Before(*e.NextAttemptAt)) {
			event = e
			break
		}
	}
	if event.ID == "" {
		m.mu.Unlock()
		return
	}
	storeID := m.state.StoreID
	revision := m.state.Revision
	m.mu.Unlock()
	if event.Kind != "test" {
		request, cancel := context.WithTimeout(ctx, 10*time.Second)
		resolution, err := m.resolve(request, event.Target)
		cancel()
		if err != nil {
			return
		}
		if !resolution.Available || waiting(event.Kind) && (!resolution.Current || resolution.State != event.State) || event.Kind == "task_unknown" && resolution.State != "unknown" {
			m.finishWithoutSend(storeID, revision, event.ID, "superseded")
			return
		}
	}
	m.mu.Lock()
	if m.state.StoreID != storeID || m.state.Revision != revision || !m.state.Config.Enabled || m.failure != "" {
		m.mu.Unlock()
		return
	}
	next := clone(m.state)
	found := -1
	for i, e := range next.Events {
		if e.ID == event.ID && (e.Status == "pending" || e.Status == "uncertain" && e.NextAttemptAt != nil) {
			found = i
			break
		}
	}
	if found < 0 {
		m.mu.Unlock()
		return
	}
	next.Events[found].Status = "sending"
	next.Events[found].Attempts++
	next.Events[found].NextAttemptAt = nil
	if m.save(next) != nil {
		// Publication can succeed before its durability confirmation fails.
		// This claim made no HTTP request, so restore that exact previous
		// receipt before another collection can clear the storage error.
		for _, claimed := range m.state.Events {
			if claimed.ID == event.ID && claimed.Status == "sending" && claimed.Attempts == event.Attempts+1 {
				recovery := clone(m.state)
				for i := range recovery.Events {
					if recovery.Events[i].ID == event.ID {
						recovery.Events[i] = event
					}
				}
				m.completion = &recovery
				break
			}
		}
		m.mu.Unlock()
		return
	}
	event = next.Events[found]
	config := next.Config
	token := next.Token
	send, cancel := context.WithCancel(ctx)
	m.sendCancel = cancel
	m.mu.Unlock()
	outcome, retry := m.send(send, storeID, config, token, event)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCancel = nil
	next = clone(m.state)
	for i := range next.Events {
		e := &next.Events[i]
		if e.ID != event.ID {
			continue
		}
		if e.Status != "sending" {
			return
		}
		e.Status = outcome
		switch outcome {
		case "accepted":
			e.Error = ""
		case "pending":
			e.Error = "quota_exhausted"
			t := m.now().Add(retry)
			next.NextSendAt = &t
		case "uncertain":
			e.Error = "response_uncertain"
		default:
			e.Error = "provider_rejected"
		}
		if e.Attempts < maxAttempts && (outcome == "pending" || outcome == "uncertain") {
			if retry <= 0 {
				retry = time.Duration(e.Attempts) * time.Minute
			}
			t := m.now().Add(retry)
			e.NextAttemptAt = &t
		} else if outcome == "pending" {
			e.Status = "failed"
		}
		break
	}
	if m.save(next) != nil {
		m.completion = &next
	}
}

func (m *Manager) finishWithoutSend(id string, revision uint64, eventID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.check(id, revision) != nil {
		return
	}
	next := clone(m.state)
	for i := range next.Events {
		if next.Events[i].ID == eventID {
			next.Events[i].Status = status
			next.Events[i].NextAttemptAt = nil
		}
	}
	_ = m.save(next)
}

func (m *Manager) resolve(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	if m.options.Resolve == nil {
		return notificationevent.Resolution{Target: target}, nil
	}
	return m.options.Resolve(ctx, target)
}

func (m *Manager) Detail(ctx context.Context, storeID, id string) (Notification, notificationevent.Resolution, error) {
	m.mu.Lock()
	if m.currentStore() != nil {
		m.mu.Unlock()
		return Notification{}, notificationevent.Resolution{}, ErrStore
	}
	if storeID != m.state.StoreID {
		m.mu.Unlock()
		return Notification{}, notificationevent.Resolution{}, ErrConflict
	}
	var event Notification
	for _, e := range m.state.Events {
		if e.ID == id {
			event = e
			break
		}
	}
	m.mu.Unlock()
	if event.ID == "" {
		return event, notificationevent.Resolution{}, ErrMissing
	}
	if event.Kind == "test" {
		return event, notificationevent.Resolution{State: "test"}, nil
	}
	r, err := m.resolve(ctx, event.Target)
	if err != nil {
		r = notificationevent.Resolution{Target: event.Target, State: "unavailable"}
	}
	return event, r, nil
}

func (m *Manager) Link(storeID, id, origin string) string {
	return strings.TrimSuffix(origin, "/") + "/?notification=" + storeID + ":" + id
}
