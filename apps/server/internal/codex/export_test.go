package codex

import (
	"context"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// TestRegisterHTTP connects private service fixtures to the real child adapter.
// The external test package sets this before any tests run, avoiding a production
// service-to-transport dependency and avoiding a duplicate test HTTP adapter.
var TestRegisterHTTP func(*http.ServeMux, *Manager)

func (m *Manager) Register(mux *http.ServeMux) {
	if TestRegisterHTTP == nil {
		panic("Codex HTTP test adapter is not registered")
	}
	TestRegisterHTTP(mux, m)
}

// Native fixtures inspect and seed their explicitly isolated tmux server.
func paneTarget(session storage.CodexSession) string       { return runtimeTarget(session).PaneTarget() }
func tmuxArguments(socket string, args ...string) []string { return tmux.Arguments(socket, args...) }
func (m *Manager) tmux(ctx context.Context, path string, args ...string) (string, error) {
	return m.backend().Command(ctx, path, tmux.Target{Socket: m.socket}, args...)
}
