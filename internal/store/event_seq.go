package store

import (
	"context"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// EventSequencer is the optional store that numbers events itself. EnqueueEvent stores the
// Seq its caller chose, which on a queue shared by replicas is that replica's clock, so
// skewed clocks can order two events of a repo wrongly. EnqueueSequenced ignores e.Seq and
// gives the event the next number of one sequence all replicas draw from, so Seq order is
// arrival order at the store whatever the clocks say. The sequence starts above the Seq of
// every event stored before it existed (at the clock at first use, or at its migration), so
// older rows keep sorting first. All of this module's stores implement it.
type EventSequencer interface {
	// EnqueueSequenced is EnqueueEvent with the store's own Seq. It returns the event as
	// stored (Seq set) and whether it was new; a duplicate ID changes nothing and returns
	// false.
	EnqueueSequenced(ctx context.Context, e model.Event) (model.Event, bool, error)
}

// firstEventSeq is where a sequence with no history starts: the clock, which sorts after
// the clock-assigned Seq of every event written before the sequence existed.
func firstEventSeq() int64 { return time.Now().UnixNano() }
