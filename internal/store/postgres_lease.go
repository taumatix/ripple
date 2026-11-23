package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taumatix/ripple/internal/model"
)

// Expiry is computed by the database (now() + ttl), so replicas' clocks never matter.
// The upsert's WHERE is what makes one racer win: the conflicting row is locked, and a
// loser re-checks it against the winner's fresh expiry and updates nothing.
func (p *Postgres) AcquireLease(ctx context.Context, name, owner string, ttl time.Duration) (Lease, bool, error) {
	l := Lease{Name: name, Owner: owner}
	err := p.pool.QueryRow(ctx, `INSERT INTO leases(name,owner,token,expires) VALUES($1,$2,1,now()+make_interval(secs => $3))
ON CONFLICT (name) DO UPDATE SET
  token = CASE WHEN leases.owner = EXCLUDED.owner AND leases.expires > now() THEN leases.token ELSE leases.token + 1 END,
  owner = EXCLUDED.owner,
  expires = EXCLUDED.expires
WHERE leases.expires <= now() OR leases.owner = EXCLUDED.owner
RETURNING token, expires`, name, owner, ttl.Seconds()).Scan(&l.Token, &l.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, wrap("acquire lease", err)
	}
	l.Expires = l.Expires.UTC()
	return l, true, nil
}

func (p *Postgres) RenewLease(ctx context.Context, l Lease, ttl time.Duration) (Lease, bool, error) {
	err := p.pool.QueryRow(ctx, `UPDATE leases SET expires = now()+make_interval(secs => $4)
WHERE name=$1 AND owner=$2 AND token=$3 RETURNING expires`, l.Name, l.Owner, l.Token, ttl.Seconds()).Scan(&l.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, wrap("renew lease", err)
	}
	l.Expires = l.Expires.UTC()
	return l, true, nil
}

func (p *Postgres) ReleaseLease(ctx context.Context, l Lease) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM leases WHERE name=$1 AND owner=$2 AND token=$3`, l.Name, l.Owner, l.Token)
	return wrap("release lease", err)
}

func (p *Postgres) GetEvent(ctx context.Context, id string) (model.Event, bool, error) {
	evs, err := p.queryEvents(ctx, "get event", `SELECT `+eventCols+` FROM events WHERE id=$1`, id)
	if err != nil || len(evs) == 0 {
		return model.Event{}, false, err
	}
	return evs[0], true, nil
}
