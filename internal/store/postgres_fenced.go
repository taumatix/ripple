package store

import (
	"context"

	"github.com/taumatix/ripple/internal/model"
)

// SetCursorFenced is store.Store.
func (p *Postgres) SetCursorFenced(ctx context.Context, n, v string, l Lease) error {
	tag, err := p.pool.Exec(ctx, `INSERT INTO cursors(name,value)
SELECT $1,$2 WHERE EXISTS (SELECT 1 FROM leases WHERE name=$3 AND owner=$4 AND token=$5)
ON CONFLICT (name) DO UPDATE SET value=$2`, n, v, l.Name, l.Owner, l.Token)
	if err != nil {
		return wrap("set cursor", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// UpdateEventFenced is store.Store. A statement that touches no row is ambiguous (the
// event is gone, or the lease is not ours), so a second read tells them apart.
func (p *Postgres) UpdateEventFenced(ctx context.Context, e model.Event, l Lease) error {
	tag, err := p.pool.Exec(ctx, `UPDATE events SET attempts=$2, not_before=$3, last_error=$4, last_attempt_at=$5, dead_at=$6
WHERE id=$1 AND EXISTS (SELECT 1 FROM leases WHERE name=$7 AND owner=$8 AND token=$9)`,
		e.ID, e.Attempts, e.NotBefore, e.LastError, nullTime(e.LastAttemptAt), nullTime(e.DeadAt), l.Name, l.Owner, l.Token)
	if err != nil {
		return wrap("update event", err)
	}
	if tag.RowsAffected() == 0 {
		return p.leaseOrGone(ctx, l)
	}
	return nil
}

// DeleteEventFenced is store.Store.
func (p *Postgres) DeleteEventFenced(ctx context.Context, id string, l Lease) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM events WHERE id=$1 AND EXISTS (SELECT 1 FROM leases WHERE name=$2 AND owner=$3 AND token=$4)`,
		id, l.Name, l.Owner, l.Token)
	if err != nil {
		return wrap("delete event", err)
	}
	if tag.RowsAffected() == 0 {
		return p.leaseOrGone(ctx, l)
	}
	return nil
}

// leaseOrGone is what a fenced write that changed no row returns: ErrLeaseLost when l is
// not the live grant, nil when it is and the row was simply absent.
func (p *Postgres) leaseOrGone(ctx context.Context, l Lease) error {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM leases WHERE name=$1 AND owner=$2 AND token=$3)`,
		l.Name, l.Owner, l.Token).Scan(&ok)
	if err != nil {
		return wrap("check lease", err)
	}
	if !ok {
		return ErrLeaseLost
	}
	return nil
}
