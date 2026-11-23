// Package store defines the persistence interface and its implementations.
package store

import (
	"context"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// Store is the persistence contract. The performance-critical operation is
// ListDependents: package -> every repo importing it, with the current version.
type Store interface {
	UpsertRepo(ctx context.Context, r model.Repo) error
	GetRepo(ctx context.Context, fullName string) (model.Repo, bool, error)
	// DeleteRepo removes the repo and all of its dependencies.
	DeleteRepo(ctx context.Context, fullName string) error

	// ReplaceDependencies makes deps the complete set for repo.
	ReplaceDependencies(ctx context.Context, repo string, deps []model.Dependency) error
	ListRepoDependencies(ctx context.Context, repo string) ([]model.Dependency, error)
	// ListDependents is the reverse index lookup. pkg may be any major version of
	// the package: the result covers the whole line (see model.Line).
	ListDependents(ctx context.Context, datasource, pkg string) ([]model.Dependency, error)

	// RecordRelease stores a discovered version; reports whether it was new.
	RecordRelease(ctx context.Context, r model.Release) (bool, error)
	// ListReleases returns the stored releases of exactly this package (not the rest of its
	// line), ordered by version string. It reads what RecordRelease stored; it never asks a feed.
	ListReleases(ctx context.Context, datasource, pkg string) ([]model.Release, error)

	// EnqueueJob inserts the job unless its key exists; reports whether inserted.
	EnqueueJob(ctx context.Context, j model.Job) (bool, error)
	GetJob(ctx context.Context, key string) (model.Job, bool, error)
	UpdateJob(ctx context.Context, j model.Job) error
	// ListDueJobs returns pending jobs with NotBefore <= now, oldest first.
	ListDueJobs(ctx context.Context, now time.Time, limit int) ([]model.Job, error)
	// ListDueJobsExcluding is ListDueJobs without the jobs of the named repos, applied
	// before the limit, so repos whose run is in flight cannot fill the batch and hold
	// back every other repo's jobs.
	ListDueJobsExcluding(ctx context.Context, now time.Time, limit int, repos []string) ([]model.Job, error)

	// EnqueueEvent persists a received event unless its ID exists; reports whether inserted.
	EnqueueEvent(ctx context.Context, e model.Event) (bool, error)
	// ListEvents returns every live (not dead) queued event, lowest Seq first (ties by ID).
	ListEvents(ctx context.Context) ([]model.Event, error)
	// UpdateEvent stores Attempts, NotBefore, LastError, LastAttemptAt and DeadAt for an
	// existing event; setting DeadAt moves it out of ListEvents into ListDeadEvents. An
	// absent ID is not an error.
	UpdateEvent(ctx context.Context, e model.Event) error
	// DeleteEvent removes an event, live or dead; an absent ID is not an error.
	DeleteEvent(ctx context.Context, id string) error
	// ListDeadEvents returns the dead-lettered events, earliest DeadAt first (ties by ID).
	ListDeadEvents(ctx context.Context) ([]model.Event, error)
	// RequeueDeadEvent makes a dead event live again with Attempts 0 and no delay (LastError
	// is kept); reports whether a dead event with that ID existed.
	RequeueDeadEvent(ctx context.Context, id string) (bool, error)
	// PurgeDeadEvents deletes the dead events whose DeadAt is before the given time and
	// returns how many.
	PurgeDeadEvents(ctx context.Context, before time.Time) (int, error)

	GetCursor(ctx context.Context, name string) (string, error)
	SetCursor(ctx context.Context, name, value string) error

	Close() error
}

// OpenJobLister is the optional listing of a repo's jobs that still need attention:
// pending (due or deferred) and failed, in no particular order. The dependency
// dashboard reads it; a store without it simply has no dashboard. Every store in
// this package implements it.
type OpenJobLister interface {
	ListOpenJobs(ctx context.Context, repo string) ([]model.Job, error)
}
