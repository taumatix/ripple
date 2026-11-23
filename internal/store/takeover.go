package store

import (
	"log/slog"
	"sync/atomic"
)

var takeovers atomic.Int64

// LeaseTakeovers is how many leases this process has taken over from a holder that died or
// stalled past its ttl since it started. A healthy deployment stays at zero; a count that
// climbs means replicas are crashing, being paused, or running with leases too short.
func LeaseTakeovers() int64 { return takeovers.Load() }

// NoteTakeover counts and logs the takeover of l by owner. log may be nil.
func NoteTakeover(log *slog.Logger, l Lease, owner string) {
	takeovers.Add(1)
	if log != nil {
		log.Info("lease taken over", "lease", l.Name, "owner", owner, "token", l.Token)
	}
}
