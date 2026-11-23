package store

import (
	"context"
	"strings"
	"time"
)

// ClaimDelivery is store.DeliveryTable: one upsert that takes the row only when it is new
// or expired, so of racing replicas exactly one gets a row back.
func (p *Postgres) ClaimDelivery(ctx context.Context, key string, now time.Time, ttl time.Duration) (bool, error) {
	tag, err := p.pool.Exec(ctx, `INSERT INTO deliveries(key, expires_at) VALUES($1,$2)
ON CONFLICT (key) DO UPDATE SET expires_at=$2 WHERE deliveries.expires_at <= $3`, key, now.Add(ttl), now)
	if err != nil {
		return false, wrap("claim delivery", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseDelivery is store.DeliveryTable.
func (p *Postgres) ReleaseDelivery(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM deliveries WHERE key=$1`, key)
	return wrap("release delivery", err)
}

// PurgeDeliveries is store.DeliveryTable.
func (p *Postgres) PurgeDeliveries(ctx context.Context, before time.Time) (int, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM deliveries WHERE expires_at < $1`, before)
	return int(tag.RowsAffected()), wrap("purge deliveries", err)
}

// PurgeCursors is store.CursorPurger. The prefix is matched with starts_with, not LIKE, so
// a % or _ in it means itself.
func (p *Postgres) PurgeCursors(ctx context.Context, prefix string, drop func(string) bool) (int, error) {
	rows, err := p.pool.Query(ctx, `SELECT name, value FROM cursors WHERE starts_with(name, $1)`, prefix)
	if err != nil {
		return 0, wrap("purge cursors", err)
	}
	type row struct{ name, value string }
	var drops []row
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			rows.Close()
			return 0, wrap("purge cursors", err)
		}
		if strings.HasPrefix(name, prefix) && drop(value) {
			drops = append(drops, row{name, value})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, wrap("purge cursors", err)
	}
	n := 0
	for _, r := range drops {
		// Conditional on the value read, so a cursor rewritten since stays.
		tag, err := p.pool.Exec(ctx, `DELETE FROM cursors WHERE name=$1 AND value=$2`, r.name, r.value)
		if err != nil {
			return n, wrap("purge cursors", err)
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}
