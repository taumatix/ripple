package store

import (
	"context"
	"sort"

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
		out = append(out, copyEvent(e))
	}
	SortEvents(out)
	return out, nil
}

func (m *Memory) UpdateEvent(_ context.Context, e model.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.events[e.ID]; ok {
		old.Attempts, old.NotBefore = e.Attempts, e.NotBefore
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
