package control

import (
	"context"
	"database/sql"
	"slices"
	"sync"

	"golang.org/x/sync/semaphore"
)

// Each resource has a fair, cancellable reader/writer lease. Guest I/O must
// fence lifecycle admission for its exact VM and volume without holding a SQL
// transaction or delaying unrelated resources and controller observations.
type resourceLeases struct {
	mu      sync.Mutex
	entries map[string]*resourceLease
}

type resourceLease struct {
	gate *semaphore.Weighted
	refs int
}

const exclusiveLease int64 = 1 << 60

func (l *resourceLeases) acquire(ctx context.Context, exclusive bool, ids ...string) (func(), error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == "" })
	weight := int64(1)
	if exclusive {
		weight = exclusiveLease
	}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*resourceLease)
	}
	entries := make([]*resourceLease, len(ids))
	for i, id := range ids {
		entry := l.entries[id]
		if entry == nil {
			entry = &resourceLease{gate: semaphore.NewWeighted(exclusiveLease)}
			l.entries[id] = entry
		}
		entry.refs++
		entries[i] = entry
	}
	l.mu.Unlock()
	acquired := 0
	release := func() {
		for i := acquired - 1; i >= 0; i-- {
			entries[i].gate.Release(weight)
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		for i, entry := range entries {
			entry.refs--
			if entry.refs == 0 {
				delete(l.entries, ids[i])
			}
		}
	}
	// Sorted acquisition prevents cycles for requests sharing both VM and volume.
	for _, entry := range entries {
		if err := entry.gate.Acquire(ctx, weight); err != nil {
			release()
			return nil, err
		}
		acquired++
	}
	return release, nil
}

// Once admission commits, durable operation locks fence subsequent file access
// until completion. Workers need no in-memory lease while executing providers.
func (s *Store) resourceTransaction(ctx context.Context, ids []string, fn func(*sql.Tx) error) error {
	release, err := s.projectLeases.acquire(ctx, true, ids...)
	if err != nil {
		return err
	}
	defer release()
	return s.transaction(ctx, fn)
}
