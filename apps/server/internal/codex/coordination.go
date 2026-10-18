package codex

import (
	"context"
	"sync"
)

type runGate struct {
	token chan struct{}
	users int
}

// Gates serialize one run and release their memory when no callers remain.
type runGates struct {
	mu    sync.Mutex
	gates map[string]*runGate
}

func (g *runGates) acquire(ctx context.Context, id string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[string]*runGate)
	}
	gate := g.gates[id]
	if gate == nil {
		gate = &runGate{token: make(chan struct{}, 1)}
		g.gates[id] = gate
	}
	gate.users++
	g.mu.Unlock()
	forget := func() {
		g.mu.Lock()
		gate.users--
		if gate.users == 0 {
			delete(g.gates, id)
		}
		g.mu.Unlock()
	}
	select {
	case gate.token <- struct{}{}:
		release := func() { <-gate.token; forget() }
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

func (m *Manager) connectionCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.connections[id])
}
