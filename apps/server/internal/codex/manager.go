// Package codex owns local persistent Codex conversations, runs and attachments.
package codex

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type Manager struct {
	store         *storage.Store
	databaseID    string
	socket        string
	namePrefix    string
	settings      Options
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	wg            sync.WaitGroup
	closed        bool
	runGates      runGates
	inputGates    runGates
	copyModes     sync.Map
	attachments   int
	leases        map[*Attachment]bool
	connections   map[string]map[*Attachment]bool
	activityMu    sync.Mutex
	activityEpoch string
	activityRead  uint64
	runnerCommand func(string) ([]string, error)
	verifyHistory func(context.Context, string, string, string, []string, string) error
	probe         func(context.Context) Info
	run           func(context.Context, string, ...string) (string, error)
}

func New(store *storage.Store, settings Options) (*Manager, error) {
	if settings.RunnerExecutable != "" && !filepath.IsAbs(settings.RunnerExecutable) {
		return nil, fmt.Errorf("Codex runner executable must be absolute")
	}
	dir, err := filepath.Abs(settings.DataDir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	id, err := store.DatabaseID(context.Background())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:         store,
		databaseID:    id,
		settings:      settings,
		socket:        settings.TmuxSocket,
		namePrefix:    "axv-codex-" + instanceID(id, dir),
		ctx:           ctx,
		cancel:        cancel,
		connections:   make(map[string]map[*Attachment]bool),
		leases:        make(map[*Attachment]bool),
		activityEpoch: rand.Text(),
		probe:         probe,
		run:           command,
		verifyHistory: verifyCodexHistory,
	}
	m.runnerCommand = m.defaultRunnerCommand
	return m, nil
}

// Done closes when the service starts shutting down.
func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }

// Close cancels service operations and detaches clients without ending runs.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	leases := make([]*Attachment, 0, len(m.leases))
	for lease := range m.leases {
		leases = append(leases, lease)
	}
	m.mu.Unlock()
	for _, lease := range leases {
		lease.Close()
	}
	m.wg.Wait()
}

// begin admits ordinary service work before shutdown starts waiting.
func (m *Manager) begin(parent context.Context) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, failure(Unavailable, "server_stopping", "AXV is stopping. Try again after restarting it.")
	}
	m.wg.Add(1)
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(m.ctx, cancel)
	return ctx, func() { stop(); cancel(); m.wg.Done() }, nil
}

// DatabaseID is the immutable desktop store identity captured at startup.
func (m *Manager) DatabaseID() string { return m.databaseID }
