package store

import (
	"context"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

func (p *Postgres) EnqueueEvent(ctx context.Context, e model.Event) (bool, error) {
	files := e.Files
	if files == nil {
		files = []string{}
	}
	tag, err := p.pool.Exec(ctx, `INSERT INTO events(id,seq,installation_id,kind,repo,ref,files,attempts,not_before,new_repo,default_branch,repo_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (id) DO NOTHING`,
		e.ID, e.Seq, e.InstallationID, e.Kind, e.Repo, e.Ref, files, e.Attempts, e.NotBefore, e.NewRepo, e.DefaultBranch, e.RepoID)
	return tag.RowsAffected() == 1, wrap("enqueue event", err)
}

const eventCols = `id,seq,installation_id,kind,repo,ref,files,attempts,not_before,last_error,last_attempt_at,dead_at,new_repo,default_branch,repo_id`

func (p *Postgres) queryEvents(ctx context.Context, op, query string, args ...any) ([]model.Event, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var last, dead *time.Time
		if err := rows.Scan(&e.ID, &e.Seq, &e.InstallationID, &e.Kind, &e.Repo, &e.Ref, &e.Files, &e.Attempts, &e.NotBefore, &e.LastError, &last, &dead, &e.NewRepo, &e.DefaultBranch, &e.RepoID); err != nil {
			return nil, wrap(op, err)
		}
		if len(e.Files) == 0 {
			e.Files = nil
		}
		e.NotBefore = e.NotBefore.UTC()
		if last != nil {
			e.LastAttemptAt = last.UTC()
		}
		if dead != nil {
			e.DeadAt = dead.UTC()
		}
		out = append(out, e)
	}
	return out, wrap(op, rows.Err())
}

func (p *Postgres) ListEvents(ctx context.Context) ([]model.Event, error) {
	return p.queryEvents(ctx, "list events", `SELECT `+eventCols+` FROM events WHERE dead_at IS NULL ORDER BY seq, id`)
}

func (p *Postgres) ListDeadEvents(ctx context.Context) ([]model.Event, error) {
	return p.queryEvents(ctx, "list dead events", `SELECT `+eventCols+` FROM events WHERE dead_at IS NOT NULL ORDER BY dead_at, id`)
}

// nullTime stores the zero time as NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (p *Postgres) UpdateEvent(ctx context.Context, e model.Event) error {
	_, err := p.pool.Exec(ctx, `UPDATE events SET attempts=$2, not_before=$3, last_error=$4, last_attempt_at=$5, dead_at=$6 WHERE id=$1`,
		e.ID, e.Attempts, e.NotBefore, e.LastError, nullTime(e.LastAttemptAt), nullTime(e.DeadAt))
	return wrap("update event", err)
}

func (p *Postgres) RequeueDeadEvent(ctx context.Context, id string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `UPDATE events SET attempts=0, not_before=$2, dead_at=NULL WHERE id=$1 AND dead_at IS NOT NULL`, id, time.Time{})
	return tag.RowsAffected() == 1, wrap("requeue dead event", err)
}

func (p *Postgres) PurgeDeadEvents(ctx context.Context, before time.Time) (int, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM events WHERE dead_at IS NOT NULL AND dead_at < $1`, before)
	return int(tag.RowsAffected()), wrap("purge dead events", err)
}

func (p *Postgres) DeleteEvent(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM events WHERE id=$1`, id)
	return wrap("delete event", err)
}
