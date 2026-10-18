package codex

import (
	"context"
	"fmt"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
)

type process = tmux.Process

type inputFrame struct {
	data       []byte
	cols, rows int
	scroll     *int
}

func (m *Manager) applyFrame(ctx context.Context, p process, path, id string, frame inputFrame) error {
	release, err := m.inputGates.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	if frame.cols != 0 {
		if p.Resize(frame.cols, frame.rows) != nil {
			return fmt.Errorf("Could not resize the Codex terminal.")
		}
		return m.waitSize(ctx, path, id, frame.cols, frame.rows)
	}
	if frame.scroll != nil {
		return m.scroll(ctx, path, id, *frame.scroll)
	}
	if copying, _ := m.copyModes.Load(id); copying == true {
		if err := m.scroll(ctx, path, id, 0); err != nil {
			return err
		}
	}
	for len(frame.data) > 0 {
		n, err := p.Write(frame.data)
		if err != nil || n == 0 {
			return fmt.Errorf("Codex stopped accepting terminal input.")
		}
		frame.data = frame.data[n:]
	}
	return nil
}

func (m *Manager) scroll(ctx context.Context, path, id string, lines int) error {
	session, err := m.store.CodexRunByID(ctx, id)
	if err != nil {
		return err
	}
	if final(session.Status) {
		return fmt.Errorf("This Codex run has ended.")
	}
	copying, err := m.backend().Scroll(ctx, path, runtimeTarget(session), lines)
	if err == nil {
		m.copyModes.Store(id, copying)
	}
	return err
}

func (m *Manager) waitSize(ctx context.Context, path, id string, cols, rows int) error {
	session, err := m.store.CodexRunByID(ctx, id)
	if err != nil {
		return err
	}
	if final(session.Status) {
		return fmt.Errorf("This Codex run has ended.")
	}
	return m.backend().WaitSize(ctx, path, runtimeTarget(session), cols, rows)
}
