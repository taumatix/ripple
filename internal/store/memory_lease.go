package store

import (
	"context"
	"time"
)

func (m *Memory) AcquireLease(_ context.Context, name, owner string, ttl time.Duration) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases == nil {
		m.leases = map[string]Lease{}
	}
	now := time.Now()
	cur, held := m.leases[name]
	switch {
	case !held:
		cur = Lease{Name: name, Owner: owner, Token: 1}
	case cur.Owner == owner && cur.Expires.After(now):
		// Still ours: extend, keep the token.
	case cur.Owner == owner || !cur.Expires.After(now):
		cur = Lease{Name: name, Owner: owner, Token: cur.Token + 1}
	default:
		return Lease{}, false, nil
	}
	cur.Expires = now.Add(ttl)
	m.leases[name] = cur
	return cur, true, nil
}

func (m *Memory) RenewLease(_ context.Context, l Lease, ttl time.Duration) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.leases[l.Name]
	if !ok || cur.Owner != l.Owner || cur.Token != l.Token {
		return Lease{}, false, nil
	}
	cur.Expires = time.Now().Add(ttl)
	m.leases[l.Name] = cur
	return cur, true, nil
}

func (m *Memory) ReleaseLease(_ context.Context, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.leases[l.Name]; ok && cur.Owner == l.Owner && cur.Token == l.Token {
		delete(m.leases, l.Name)
	}
	return nil
}
