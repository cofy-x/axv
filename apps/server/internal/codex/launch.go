package codex

import (
	"context"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func (m *Manager) start(ctx context.Context, info Info, session storage.CodexSession, cols, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runner, err := m.prepareRunner(info, session)
	if err != nil {
		return err
	}
	env, _ := m.environment(ctx, filepath.Dir(info.CodexPath))
	return m.backend().Start(ctx, info.TmuxPath, tmux.Launch{
		Target: runtimeTarget(session), RunID: session.RunID, ProjectPath: session.ProjectPath,
		Command: runner, Environment: env, Cols: cols, Rows: rows,
		OnSocket: func(ctx context.Context, socket string) error {
			return m.store.SetCodexRunSocket(ctx, session.RunID, socket)
		},
	})
}
