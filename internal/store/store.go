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

	// EnqueueJob inserts the job unless its key exists; reports whether inserted.
	EnqueueJob(ctx context.Context, j model.Job) (bool, error)
	GetJob(ctx context.Context, key string) (model.Job, bool, error)
	UpdateJob(ctx context.Context, j model.Job) error
	// ListDueJobs returns pending jobs with NotBefore <= now, oldest first.
	ListDueJobs(ctx context.Context, now time.Time, limit int) ([]model.Job, error)

	GetCursor(ctx context.Context, name string) (string, error)
	SetCursor(ctx context.Context, name, value string) error

	Close() error
}
