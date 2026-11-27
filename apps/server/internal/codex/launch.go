package codex

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func mergeEnvironment(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	override := make(map[string]bool, len(extra))
	for _, entry := range extra {
		if key, _, ok := strings.Cut(entry, "="); ok && key != "" {
			override[key] = true
		}
	}
	merged := make([]string, 0, len(base)+len(extra))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !override[key] {
			merged = append(merged, entry)
		}
	}
	return append(merged, extra...)
}

func (m *Manager) start(ctx context.Context, info Info, session storage.CodexSession, cols, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runner, err := m.prepareRunner(info, session)
	if err != nil {
		return err
	}
	env, _ := m.environment(ctx, filepath.Dir(info.CodexPath))
	if m.settings.ExtraEnv != nil {
		env = mergeEnvironment(env, m.settings.ExtraEnv())
	}
	return m.backend().Start(ctx, info.TmuxPath, tmux.Launch{
		Target: runtimeTarget(session), RunID: session.RunID, ProjectPath: session.ProjectPath,
		Command: runner, Environment: env, Cols: cols, Rows: rows,
		OnSocket: func(ctx context.Context, socket string) error {
			return m.store.SetCodexRunSocket(ctx, session.RunID, socket)
		},
	})
}
