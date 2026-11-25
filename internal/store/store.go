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
	Leases

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
	// UpdateJobFenced is UpdateJob that applies only while l is still the live lease
	// (name, owner and fencing token all unchanged), atomically with the write, and
	// otherwise changes nothing and returns ErrLeaseLost. A replica that lapsed and was
	// replaced cannot overwrite the job row the new holder owns.
	UpdateJobFenced(ctx context.Context, j model.Job, l Lease) error
	// SetCursorFenced is SetCursor that applies only while l is still the live lease
	// (same owner and token), and otherwise changes nothing and returns ErrLeaseLost.
	SetCursorFenced(ctx context.Context, name, value string, l Lease) error
	// UpdateEventFenced is UpdateEvent under the same rule as UpdateJobFenced. An event
	// that no longer exists is a no-op, as in UpdateEvent.
	UpdateEventFenced(ctx context.Context, e model.Event, l Lease) error
	// DeleteEventFenced is DeleteEvent under the same rule. A missing event is not an error.
	DeleteEventFenced(ctx context.Context, id string, l Lease) error
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
	// HasOlderEvent reports whether a live (not dead) event of e.Repo sorts before e by
	// (Seq, ID). It is how a replica that claimed e learns another replica's older event
	// for the same repo is still queued, so per-repo order holds across replicas.
	HasOlderEvent(ctx context.Context, e model.Event) (bool, error)
	// GetEvent returns one event, live or dead; the bool is false when it does not exist.
	GetEvent(ctx context.Context, id string) (model.Event, bool, error)
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

// RepoLister is the optional paged listing of registered repos, by name. ListRepos returns
// up to limit names greater than after, in ascending order; an empty after starts at the
// beginning, and fewer than limit names means the end. The advisory sweep reads it. The
// memory and Postgres stores implement it; DynamoDB has no cheap listing and does not.
type RepoLister interface {
	ListRepos(ctx context.Context, after string, limit int) ([]string, error)
}

// DeliveryTable is the optional table of webhook delivery ids that were already handled,
// shared by every replica and surviving a restart. Every store in this package implements
// it; a store without it leaves the receivers on their in-memory table.
type DeliveryTable interface {
	// ClaimDelivery reserves key until now+ttl. It returns false, changing nothing, when
	// the key is held and has not expired: the delivery is a repeat. Of several callers
	// racing for one key exactly one gets true.
	ClaimDelivery(ctx context.Context, key string, now time.Time, ttl time.Duration) (bool, error)
	// ReleaseDelivery forgets key, so a delivery whose handling failed can be redelivered.
	// An absent key is not an error.
	ReleaseDelivery(ctx context.Context, key string) error
	// PurgeDeliveries deletes the keys whose expiry is before the given time and returns
	// how many. Expired keys are ignored by ClaimDelivery whether or not they were purged.
	PurgeDeliveries(ctx context.Context, before time.Time) (int, error)
}

// CursorPurger is the optional deletion of stale cursors by name prefix. PurgeCursors
// deletes every cursor whose name starts with prefix and whose value drop reports true,
// and returns how many; drop must not call the store. Memory and Postgres read only the
// prefix's rows; DynamoDB has no key listing across partitions and scans the table.
type CursorPurger interface {
	PurgeCursors(ctx context.Context, prefix string, drop func(value string) bool) (int, error)
}

// Cursor is one named value of the cursor table.
type Cursor struct{ Name, Value string }

// CursorLister is the optional listing of cursors by name prefix, for state kept as one
// cursor per key instead of one shared list that replicas overwrite each other's additions
// to. ListCursors returns every cursor whose name starts with prefix. DeleteCursor removes
// one; an absent name is not an error.
//
// A name of the form "<group>#<key>" is indexed by its group on DynamoDB, so a prefix that
// includes the "#" (such as "docker:packages#") is one Query of the group; the listing then
// reads the index and is eventually consistent. Any other prefix is a scan of the table, as
// PurgeCursors is, and is meant for rare callers. Memory and Postgres read only the prefix's
// rows, current.
type CursorLister interface {
	ListCursors(ctx context.Context, prefix string) ([]Cursor, error)
	DeleteCursor(ctx context.Context, name string) error
}

// PagedCursorLister is the optional bounded form of CursorLister.ListCursors. ListCursorsPage
// returns at most limit cursors whose name starts with prefix and sorts (bytewise) after
// the name after, in name order; an empty after starts at the beginning, and an empty page
// means the listing is done. Feeding the last name of one page back as after walks the set
// without holding it whole.
//
// For a prefix containing the group separator ("docker:packages#") DynamoDB reads only the
// requested page of the group's index. Any other prefix has no key order on DynamoDB (a
// Scan), so it is listed whole and sliced, which bounds the answer but not the read.
type PagedCursorLister interface {
	ListCursorsPage(ctx context.Context, prefix, after string, limit int) ([]Cursor, error)
}
