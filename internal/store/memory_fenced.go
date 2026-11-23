package store

import (
	"context"

	"github.com/taumatix/ripple/internal/model"
)

// holds reports whether l is the live grant; m.mu held.
func (m *Memory) holds(l Lease) bool {
	cur, ok := m.leases[l.Name]
	return ok && cur.Owner == l.Owner && cur.Token == l.Token
}

// SetCursorFenced is store.Store: the lease check and the write share one critical section.
func (m *Memory) SetCursorFenced(_ context.Context, n, v string, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.holds(l) {
		return ErrLeaseLost
	}
	m.cursors[n] = v
	return nil
}

// UpdateEventFenced is store.Store.
func (m *Memory) UpdateEventFenced(ctx context.Context, e model.Event, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.holds(l) {
		return ErrLeaseLost
	}
	if old, ok := m.events[e.ID]; ok {
		old.Attempts, old.NotBefore = e.Attempts, e.NotBefore
		old.LastError, old.LastAttemptAt, old.DeadAt = e.LastError, e.LastAttemptAt, e.DeadAt
		m.events[e.ID] = old
	}
	return nil
}

// DeleteEventFenced is store.Store.
func (m *Memory) DeleteEventFenced(_ context.Context, id string, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.holds(l) {
		return ErrLeaseLost
	}
	delete(m.events, id)
	return nil
}
