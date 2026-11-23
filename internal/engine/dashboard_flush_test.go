package engine

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A feed poll ends with flushDashboards. A repo whose job is running holds the repo lock
// for as long as the job takes (a slow checkout), so the flush must skip that repo
// instead of waiting for the lock: waiting stalled the poll, and every later poll, for the
// length of the job. The repo stays marked so the next flush syncs it.
func TestFlushDashboardsDoesNotWaitForARepoARunHolds(t *testing.T) {
	ctx := context.Background()
	e := &Engine{Store: store.NewMemory()}
	e.markDashboard("acme/busy")
	unlock := e.lockRepo("acme/busy") // a job is mid-run
	done := make(chan struct{})
	go func() { e.flushDashboards(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		unlock()
		<-done
		t.Fatal("flushDashboards waited for the repo lock a running job holds")
	}
	unlock()
	if !e.storedDirty(ctx)["acme/busy"] {
		t.Fatal("the skipped repo was forgotten: nothing will sync its dashboard")
	}
}
