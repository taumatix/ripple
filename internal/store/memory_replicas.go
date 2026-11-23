package store

import (
	"context"
	"sort"
	"time"
)

type memReplica struct {
	info    ReplicaInfo
	expires time.Time
}

// Heartbeat is store.ReplicaTable.
func (m *Memory) Heartbeat(_ context.Context, r ReplicaInfo, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replicas == nil {
		m.replicas = map[string]memReplica{}
	}
	now := time.Now()
	r.Seen = now
	m.replicas[r.ID] = memReplica{info: r, expires: now.Add(ttl)}
	return nil
}

// LiveReplicas is store.ReplicaTable.
func (m *Memory) LiveReplicas(_ context.Context) ([]ReplicaInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []ReplicaInfo
	for id, r := range m.replicas {
		switch {
		case r.expires.After(now):
			out = append(out, r.info)
		case now.Sub(r.expires) > replicaGrace:
			delete(m.replicas, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RetireReplica is store.ReplicaTable.
func (m *Memory) RetireReplica(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.replicas, id)
	return nil
}
