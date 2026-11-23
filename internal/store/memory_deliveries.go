package store

import (
	"context"
	"strings"
	"time"
)

// ClaimDelivery is store.DeliveryTable.
func (m *Memory) ClaimDelivery(_ context.Context, key string, now time.Time, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deliveries == nil {
		m.deliveries = map[string]time.Time{}
	}
	if exp, ok := m.deliveries[key]; ok && now.Before(exp) {
		return false, nil
	}
	m.deliveries[key] = now.Add(ttl)
	return true, nil
}

// ReleaseDelivery is store.DeliveryTable.
func (m *Memory) ReleaseDelivery(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.deliveries, key)
	return nil
}

// PurgeDeliveries is store.DeliveryTable.
func (m *Memory) PurgeDeliveries(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, exp := range m.deliveries {
		if exp.Before(before) {
			delete(m.deliveries, k)
			n++
		}
	}
	return n, nil
}

// PurgeCursors is store.CursorPurger.
func (m *Memory) PurgeCursors(_ context.Context, prefix string, drop func(string) bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, v := range m.cursors {
		if strings.HasPrefix(k, prefix) && drop(v) {
			delete(m.cursors, k)
			n++
		}
	}
	return n, nil
}
