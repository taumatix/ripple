package store

import (
	"context"
	"time"
)

// ReplicaInfo is what one replica says about itself in its heartbeat.
type ReplicaInfo struct {
	ID string
	// Takeovers is LeaseTakeovers() of that process when it last beat.
	Takeovers  int64
	FeedPoller bool
	// Polled names the sources this replica polls now and Configured every source it has
	// configured (a name has no comma). A source configured by a live replica and polled by
	// none is one nobody polls.
	Polled, Configured []string
	// Seen is when the beat was written, by the store's clock where it has one.
	Seen time.Time
}

// ReplicaTable is the optional table of live replicas. Leases cannot answer "how many
// replicas are there": an idle replica holds none, and nobody can list the ones that do.
// Each replica instead beats on a row of its own that lapses after a ttl, so a replica that
// died drops out of LiveReplicas by itself. All of this module's stores implement it.
type ReplicaTable interface {
	// Heartbeat records r as live for ttl from now, replacing its earlier beat.
	Heartbeat(ctx context.Context, r ReplicaInfo, ttl time.Duration) error
	// LiveReplicas returns the replicas whose beat has not lapsed, ordered by ID. It also
	// drops the rows of replicas that lapsed over an hour ago, so restarts under random ids
	// do not pile up.
	LiveReplicas(ctx context.Context) ([]ReplicaInfo, error)
	// RetireReplica removes id's row at once (a clean shutdown). An absent id is not an error.
	RetireReplica(ctx context.Context, id string) error
}

// replicaGrace is how long a lapsed beat is kept before LiveReplicas removes it.
const replicaGrace = time.Hour
