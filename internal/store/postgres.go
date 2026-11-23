package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taumatix/ripple/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Postgres is the Store backed by PostgreSQL (pgx v5).
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres connects, creates schema if non-empty, and applies migrations.
func NewPostgres(ctx context.Context, dsn, schema string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if schema != "" {
		boot, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, fmt.Errorf("connect postgres: %w", err)
		}
		_, err = boot.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize())
		boot.Close(ctx)
		if err != nil {
			return nil, fmt.Errorf("create schema: %w", err)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) migrate(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Release()
	// Serialise concurrent starters (several replicas booting at once).
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(727274)"); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(727274)")
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		var done bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)", n).Scan(&done); err != nil {
			return fmt.Errorf("migrate %s: %w", n, err)
		}
		if done {
			continue
		}
		sql, _ := migrationFS.ReadFile("migrations/" + n)
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", n, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(name) VALUES($1)", n); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", n, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate %s: %w", n, err)
		}
	}
	return nil
}

func (p *Postgres) Close() error { p.pool.Close(); return nil }

func (p *Postgres) UpsertRepo(ctx context.Context, r model.Repo) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO repos(full_name, installation_id, default_branch, config)
VALUES($1,$2,$3,$4)
ON CONFLICT (full_name) DO UPDATE SET installation_id=$2, default_branch=$3, config=$4`,
		r.FullName, r.InstallationID, r.DefaultBranch, r.Config)
	return wrap("upsert repo", err)
}

func (p *Postgres) GetRepo(ctx context.Context, n string) (model.Repo, bool, error) {
	var r model.Repo
	err := p.pool.QueryRow(ctx, `SELECT full_name, installation_id, default_branch, config FROM repos WHERE full_name=$1`, n).
		Scan(&r.FullName, &r.InstallationID, &r.DefaultBranch, &r.Config)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Repo{}, false, nil
	}
	if err != nil {
		return model.Repo{}, false, wrap("get repo", err)
	}
	return r, true, nil
}

func (p *Postgres) DeleteRepo(ctx context.Context, n string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("delete repo", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM dependencies WHERE repo=$1`, n); err != nil {
		return wrap("delete repo", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM repos WHERE full_name=$1`, n); err != nil {
		return wrap("delete repo", err)
	}
	return wrap("delete repo", tx.Commit(ctx))
}

func (p *Postgres) ReplaceDependencies(ctx context.Context, repo string, deps []model.Dependency) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("replace dependencies", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM dependencies WHERE repo=$1`, repo); err != nil {
		return wrap("replace dependencies", err)
	}
	b := &pgx.Batch{}
	for _, d := range deps {
		b.Queue(`INSERT INTO dependencies(repo,file,manager,datasource,package,version,line) VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (repo,file,datasource,package) DO UPDATE SET manager=$3, version=$6, line=$7`,
			repo, d.File, d.Manager, d.Datasource, d.Package, d.Version, model.Line(d.Datasource, d.Package))
	}
	br := tx.SendBatch(ctx, b)
	for range deps {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return wrap("replace dependencies", err)
		}
	}
	if err := br.Close(); err != nil {
		return wrap("replace dependencies", err)
	}
	return wrap("replace dependencies", tx.Commit(ctx))
}

func (p *Postgres) queryDeps(ctx context.Context, sql string, args ...any) ([]model.Dependency, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Dependency
	for rows.Next() {
		var d model.Dependency
		if err := rows.Scan(&d.Repo, &d.File, &d.Manager, &d.Datasource, &d.Package, &d.Version); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) ListRepoDependencies(ctx context.Context, repo string) ([]model.Dependency, error) {
	d, err := p.queryDeps(ctx, `SELECT repo,file,manager,datasource,package,version FROM dependencies WHERE repo=$1 ORDER BY file,datasource,package`, repo)
	return d, wrap("list repo dependencies", err)
}

func (p *Postgres) ListDependents(ctx context.Context, ds, pkg string) ([]model.Dependency, error) {
	d, err := p.queryDeps(ctx, `SELECT repo,file,manager,datasource,package,version FROM dependencies WHERE datasource=$1 AND line=$2 ORDER BY repo,file`, ds, model.Line(ds, pkg))
	return d, wrap("list dependents", err)
}

func (p *Postgres) RecordRelease(ctx context.Context, r model.Release) (bool, error) {
	tag, err := p.pool.Exec(ctx, `INSERT INTO releases(datasource,package,version,released_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		r.Datasource, r.Package, r.Version, r.Time)
	return tag.RowsAffected() == 1, wrap("record release", err)
}

func (p *Postgres) ListReleases(ctx context.Context, ds, pkg string) ([]model.Release, error) {
	rows, err := p.pool.Query(ctx, `SELECT version,released_at FROM releases WHERE datasource=$1 AND package=$2 ORDER BY version`, ds, pkg)
	if err != nil {
		return nil, wrap("list releases", err)
	}
	defer rows.Close()
	var out []model.Release
	for rows.Next() {
		r := model.Release{Datasource: ds, Package: pkg}
		if err := rows.Scan(&r.Version, &r.Time); err != nil {
			return nil, wrap("list releases", err)
		}
		r.Time = r.Time.UTC()
		out = append(out, r)
	}
	return out, wrap("list releases", rows.Err())
}

func (p *Postgres) EnqueueJob(ctx context.Context, j model.Job) (bool, error) {
	tag, err := p.pool.Exec(ctx, `INSERT INTO jobs(key,repo,datasource,package,version,released_at,state,not_before,attempts,pr_url,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (key) DO NOTHING`,
		j.Key, j.Repo, j.Datasource, j.Package, j.Version, j.ReleasedAt, string(j.State), j.NotBefore, j.Attempts, j.PRURL, j.Reason)
	return tag.RowsAffected() == 1, wrap("enqueue job", err)
}

const jobCols = `key,repo,datasource,package,version,released_at,state,not_before,attempts,pr_url,reason`

func scanJob(row pgx.Row) (model.Job, error) {
	var j model.Job
	var st string
	err := row.Scan(&j.Key, &j.Repo, &j.Datasource, &j.Package, &j.Version, &j.ReleasedAt, &st, &j.NotBefore, &j.Attempts, &j.PRURL, &j.Reason)
	j.State = model.JobState(st)
	j.ReleasedAt, j.NotBefore = j.ReleasedAt.UTC(), j.NotBefore.UTC()
	return j, err
}

func (p *Postgres) GetJob(ctx context.Context, key string) (model.Job, bool, error) {
	j, err := scanJob(p.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE key=$1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Job{}, false, nil
	}
	return j, err == nil, wrap("get job", err)
}

func (p *Postgres) UpdateJob(ctx context.Context, j model.Job) error {
	_, err := p.pool.Exec(ctx, `UPDATE jobs SET state=$2, not_before=$3, attempts=$4, pr_url=$5, reason=$6 WHERE key=$1`,
		j.Key, string(j.State), j.NotBefore, j.Attempts, j.PRURL, j.Reason)
	return wrap("update job", err)
}

// ListOpenJobs is store.OpenJobLister.
func (p *Postgres) ListOpenJobs(ctx context.Context, repo string) ([]model.Job, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs WHERE repo=$1 AND state IN ('pending','failed') ORDER BY key`, repo)
	if err != nil {
		return nil, wrap("list open jobs", err)
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, wrap("list open jobs", err)
		}
		out = append(out, j)
	}
	return out, wrap("list open jobs", rows.Err())
}

func (p *Postgres) ListDueJobs(ctx context.Context, now time.Time, limit int) ([]model.Job, error) {
	return p.ListDueJobsExcluding(ctx, now, limit, nil)
}

func (p *Postgres) ListDueJobsExcluding(ctx context.Context, now time.Time, limit int, repos []string) ([]model.Job, error) {
	if repos == nil {
		repos = []string{}
	}
	rows, err := p.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs WHERE state='pending' AND not_before <= $1 AND NOT (repo = ANY($3)) ORDER BY not_before, key LIMIT $2`, now, limit, repos)
	if err != nil {
		return nil, wrap("list due jobs", err)
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, wrap("list due jobs", err)
		}
		out = append(out, j)
	}
	return out, wrap("list due jobs", rows.Err())
}

func (p *Postgres) GetCursor(ctx context.Context, n string) (string, error) {
	var v string
	err := p.pool.QueryRow(ctx, `SELECT value FROM cursors WHERE name=$1`, n).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, wrap("get cursor", err)
}

func (p *Postgres) SetCursor(ctx context.Context, n, v string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO cursors(name,value) VALUES($1,$2) ON CONFLICT (name) DO UPDATE SET value=$2`, n, v)
	return wrap("set cursor", err)
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("postgres %s: %w", op, err)
}

// UpdateJobFenced is store.Store: one statement, so the lease is checked in the same
// snapshot as the write. A row-less result means the job is missing or the lease is not
// ours; the lease is the case that matters, so both read as ErrLeaseLost.
func (p *Postgres) UpdateJobFenced(ctx context.Context, j model.Job, l Lease) error {
	tag, err := p.pool.Exec(ctx, `UPDATE jobs SET state=$2, not_before=$3, attempts=$4, pr_url=$5, reason=$6
WHERE key=$1 AND EXISTS (SELECT 1 FROM leases WHERE name=$7 AND owner=$8 AND token=$9)`,
		j.Key, string(j.State), j.NotBefore, j.Attempts, j.PRURL, j.Reason, l.Name, l.Owner, l.Token)
	if err != nil {
		return wrap("update job", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}
