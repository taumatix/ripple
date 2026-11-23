package store

import (
	"context"
	"sort"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

func copyEvent(e model.Event) model.Event {
	if len(e.Files) == 0 {
		e.Files = nil
	} else {
		e.Files = append([]string(nil), e.Files...)
	}
	return e
}

func (m *Memory) EnqueueEvent(_ context.Context, e model.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events == nil {
		m.events = map[string]model.Event{}
	}
	if _, ok := m.events[e.ID]; ok {
		return false, nil
	}
	m.events[e.ID] = copyEvent(e)
	return true, nil
}

func (m *Memory) ListEvents(_ context.Context) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Event, 0, len(m.events))
	for _, e := range m.events {
		if !e.Dead() {
			out = append(out, copyEvent(e))
		}
	}
	SortEvents(out)
	return out, nil
}

func (m *Memory) ListDeadEvents(_ context.Context) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Event
	for _, e := range m.events {
		if e.Dead() {
			out = append(out, copyEvent(e))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DeadAt.Equal(out[j].DeadAt) {
			return out[i].DeadAt.Before(out[j].DeadAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Memory) RequeueDeadEvent(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok || !e.Dead() {
		return false, nil
	}
	e.Attempts, e.NotBefore, e.DeadAt = 0, time.Time{}, time.Time{}
	m.events[id] = e
	return true, nil
}

func (m *Memory) PurgeDeadEvents(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, e := range m.events {
		if e.Dead() && e.DeadAt.Before(before) {
			delete(m.events, id)
			n++
		}
	}
	return n, nil
}

func (m *Memory) UpdateEvent(_ context.Context, e model.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.events[e.ID]; ok {
		old.Attempts, old.NotBefore = e.Attempts, e.NotBefore
		old.LastError, old.LastAttemptAt, old.DeadAt = e.LastError, e.LastAttemptAt, e.DeadAt
		m.events[e.ID] = old
	}
	return nil
}

func (m *Memory) DeleteEvent(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.events, id)
	return nil
}

// SortEvents orders events oldest first: by Seq, then ID.
func SortEvents(e []model.Event) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].Seq != e[j].Seq {
			return e[i].Seq < e[j].Seq
		}
		return e[i].ID < e[j].ID
	})
}

func (m *Memory) GetEvent(_ context.Context, id string) (model.Event, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	return copyEvent(e), ok, nil
}

// HasOlderEvent is store.Store.
func (m *Memory) HasOlderEvent(_ context.Context, e model.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.events {
		if o.Repo == e.Repo && !o.Dead() && (o.Seq < e.Seq || o.Seq == e.Seq && o.ID < e.ID) {
			return true, nil
		}
	}
	return false, nil
}

// EnqueueSequenced is store.EventSequencer. The number is drawn and the event stored under
// one lock, so no event with a higher number is visible before this one.
func (m *Memory) EnqueueSequenced(_ context.Context, e model.Event) (model.Event, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events == nil {
		m.events = map[string]model.Event{}
	}
	if _, ok := m.events[e.ID]; ok {
		return e, false, nil
	}
	m.eventSeq = max(m.eventSeq+1, firstEventSeq())
	e.Seq = m.eventSeq
	m.events[e.ID] = copyEvent(e)
	return e, true, nil
}
