package store

import (
	"context"

	"github.com/taumatix/ripple/internal/model"
)

func (p *Postgres) EnqueueEvent(ctx context.Context, e model.Event) (bool, error) {
	files := e.Files
	if files == nil {
		files = []string{}
	}
	tag, err := p.pool.Exec(ctx, `INSERT INTO events(id,seq,installation_id,kind,repo,ref,files,attempts,not_before)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
		e.ID, e.Seq, e.InstallationID, e.Kind, e.Repo, e.Ref, files, e.Attempts, e.NotBefore)
	return tag.RowsAffected() == 1, wrap("enqueue event", err)
}

func (p *Postgres) ListEvents(ctx context.Context) ([]model.Event, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,seq,installation_id,kind,repo,ref,files,attempts,not_before FROM events ORDER BY seq, id`)
	if err != nil {
		return nil, wrap("list events", err)
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.Seq, &e.InstallationID, &e.Kind, &e.Repo, &e.Ref, &e.Files, &e.Attempts, &e.NotBefore); err != nil {
			return nil, wrap("list events", err)
		}
		if len(e.Files) == 0 {
			e.Files = nil
		}
		e.NotBefore = e.NotBefore.UTC()
		out = append(out, e)
	}
	return out, wrap("list events", rows.Err())
}

func (p *Postgres) UpdateEvent(ctx context.Context, e model.Event) error {
	_, err := p.pool.Exec(ctx, `UPDATE events SET attempts=$2, not_before=$3 WHERE id=$1`, e.ID, e.Attempts, e.NotBefore)
	return wrap("update event", err)
}

func (p *Postgres) DeleteEvent(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM events WHERE id=$1`, id)
	return wrap("delete event", err)
}
