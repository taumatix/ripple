package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// EventsList prints the dead events, oldest first: the events that failed
// Queue.MaxAttempts times and are kept, with the last error, until requeued or purged.
func EventsList(ctx context.Context, st store.Store, out io.Writer) error {
	evs, err := st.ListDeadEvents(ctx)
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		fmt.Fprintln(out, "no dead events")
		return nil
	}
	for _, e := range evs {
		fmt.Fprintf(out, "%s  %s  repo=%s  attempts=%d  installation=%d  last_attempt=%s  dead=%s\n    error: %s\n",
			e.ID, e.Kind, e.Repo, e.Attempts, e.InstallationID, stamp(e.LastAttemptAt), stamp(e.DeadAt), e.LastError)
	}
	return nil
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// EventsRequeue makes the dead event id live again with a fresh attempt budget. A running
// server picks it up on its next rescan (DefaultEventRescan). It reports an error if id is
// not a dead event.
func EventsRequeue(ctx context.Context, st store.Store, id string, out io.Writer) error {
	ok, err := st.RequeueDeadEvent(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is not a dead event (see `ripple events list`)", id)
	}
	fmt.Fprintf(out, "requeued %s\n", id)
	return nil
}

// EventsRequeueAll requeues every dead event and reports how many.
func EventsRequeueAll(ctx context.Context, st store.Store, out io.Writer) error {
	evs, err := st.ListDeadEvents(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, e := range evs {
		ok, err := st.RequeueDeadEvent(ctx, e.ID)
		if err != nil {
			return fmt.Errorf("requeued %d of %d: %w", n, len(evs), err)
		}
		if ok {
			n++
		}
	}
	fmt.Fprintf(out, "requeued %d events\n", n)
	return nil
}

// EventsPurge deletes the dead events that died more than olderThan ago (zero: all of them).
func EventsPurge(ctx context.Context, st store.Store, now time.Time, olderThan time.Duration, out io.Writer) error {
	n, err := st.PurgeDeadEvents(ctx, now.Add(-olderThan))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "purged %d dead events\n", n)
	return nil
}
