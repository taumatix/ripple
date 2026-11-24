package store

import (
	"context"
	"strings"
	"time"
)

// Heartbeat is store.ReplicaTable. Both the beat time and the expiry are the database's
// now(), so replica clocks do not matter.
func (p *Postgres) Heartbeat(ctx context.Context, r ReplicaInfo, ttl time.Duration) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO replicas(id, takeovers, feed_poller, polled, configured, seen, expires)
VALUES($1,$2,$3,$5,$6,now(),now()+make_interval(secs => $4))
ON CONFLICT (id) DO UPDATE SET takeovers=EXCLUDED.takeovers, feed_poller=EXCLUDED.feed_poller,
  polled=EXCLUDED.polled, configured=EXCLUDED.configured,
  seen=EXCLUDED.seen, expires=EXCLUDED.expires`, r.ID, r.Takeovers, r.FeedPoller, ttl.Seconds(),
		strings.Join(r.Polled, ","), strings.Join(r.Configured, ","))
	return wrap("heartbeat", err)
}

// LiveReplicas is store.ReplicaTable.
func (p *Postgres) LiveReplicas(ctx context.Context) ([]ReplicaInfo, error) {
	if _, err := p.pool.Exec(ctx, `DELETE FROM replicas WHERE expires < now()-make_interval(secs => $1)`, replicaGrace.Seconds()); err != nil {
		return nil, wrap("live replicas", err)
	}
	rows, err := p.pool.Query(ctx, `SELECT id, takeovers, feed_poller, polled, configured, seen FROM replicas WHERE expires > now() ORDER BY id`)
	if err != nil {
		return nil, wrap("live replicas", err)
	}
	defer rows.Close()
	var out []ReplicaInfo
	for rows.Next() {
		var r ReplicaInfo
		var polled, configured string
		if err := rows.Scan(&r.ID, &r.Takeovers, &r.FeedPoller, &polled, &configured, &r.Seen); err != nil {
			return nil, wrap("live replicas", err)
		}
		r.Polled, r.Configured = splitSources(polled), splitSources(configured)
		r.Seen = r.Seen.UTC()
		out = append(out, r)
	}
	return out, wrap("live replicas", rows.Err())
}

// RetireReplica is store.ReplicaTable.
func (p *Postgres) RetireReplica(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM replicas WHERE id=$1`, id)
	return wrap("retire replica", err)
}

// splitSources reads a comma-separated source list; empty is no sources.
func splitSources(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
