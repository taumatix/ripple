package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A repo with more due jobs than one listing holds, whose run is in flight, must not
// hide the other repos' jobs: the listing leaves in-flight repos out before its limit.
func TestDispatchIsNotHeldBackByARepoInFlight(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	add := func(repo string, i int, nb time.Time) model.Job {
		j := model.Job{Key: model.JobKey(repo, "go", fmt.Sprintf("example.com/p%d", i), "v1.0.0"), Repo: repo, Datasource: "go",
			Package: fmt.Sprintf("example.com/p%d", i), Version: "v1.0.0", ReleasedAt: now, State: model.JobPending, NotBefore: nb}
		if _, err := st.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	for i := 0; i < 5; i++ {
		add("acme/big", i, now.Add(-time.Hour+time.Duration(i)*time.Second))
	}
	small := add("acme/small", 0, now.Add(-time.Minute))
	e := &Engine{Store: st, Now: func() time.Time { return now }, DueBatch: 3, Workers: 4}
	r := &JobRunner{Engine: e}
	r.init()
	r.inflight["acme/big"] = true // its run is still going

	r.dispatch(ctx)
	r.wg.Wait()

	got, _, _ := st.GetJob(ctx, small.Key)
	if got.State == model.JobPending {
		t.Fatalf("acme/small's job was never listed: the in-flight repo filled the batch of %d", e.DueBatch)
	}
}
