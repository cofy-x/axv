package codex

import (
	"context"
	"errors"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// Info checks host dependencies and routing, without starting a conversation.
func (m *Manager) Info(ctx context.Context) (Info, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Info{}, err
	}
	defer done()
	info := m.probe(ctx)
	info.DefaultProjectPath, info.ConfigPath = m.settings.WorkspaceDir, m.settings.ConfigPath
	_, info.Network = m.environment(ctx)
	return info, ctx.Err()
}

// List reconciles known conversations without launching or continuing them.
func (m *Manager) List(ctx context.Context) ([]Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	sessions, err := m.list(ctx)
	result := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, m.describe(session))
	}
	return result, err
}

func (m *Manager) list(ctx context.Context) ([]storage.CodexSession, error) {
	sessions, err := m.store.CodexSessions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i], err = m.visibleSession(ctx, sessions[i])
		if err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

// Runtime uncertainty preserves known identity and durable status for reads.
func (m *Manager) visibleSession(ctx context.Context, session storage.CodexSession) (storage.CodexSession, error) {
	current, err := m.reconcile(ctx, session)
	var unavailable *Error
	if errors.As(err, &unavailable) && unavailable.Kind == Unavailable {
		current.Reason = unavailable.Message
		return current, nil
	}
	return current, err
}

// Get returns the latest recorded run of a conversation.
func (m *Manager) Get(ctx context.Context, id string) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	session, err := m.store.CodexSession(ctx, id)
	if err == nil {
		session, err = m.visibleSession(ctx, session)
	}
	return m.describe(session), err
}

// Projects reads recent confirmed projects without inspecting runtime state.
func (m *Manager) Projects(ctx context.Context) ([]storage.CodexProject, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return m.store.CodexProjects(ctx)
}

// Patch updates metadata without probing, reconciling or reconnecting a run.
func (m *Manager) Patch(ctx context.Context, id string, patch storage.CodexMetadataPatch) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	session, err := m.store.PatchCodexMetadata(ctx, id, patch)
	session.Connections = m.connectionCount(session.RunID)
	return m.describe(session), err
}

func validSize(cols, rows int) bool { return cols >= 2 && cols <= 500 && rows >= 1 && rows <= 200 }
