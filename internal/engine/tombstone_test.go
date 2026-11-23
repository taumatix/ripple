package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A removal's tombstone lives in an indexed cursor group so DynamoDB sweeps it with a
// Query; tombstones written before that ("removed|<name>") are still honoured, cleared by
// a re-add and swept, until a full tombstone lifetime has passed.
func TestTombstonesMoveToAnIndexedGroupAndLegacyOnesStillWork(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Unix(1_700_000_000, 0)
	e := &Engine{Store: st, Replica: "a", LeaseTTL: time.Minute, Now: func() time.Time { return now }}
	stamp := now.Unix()
	legacy := func(name string) string { return "removed|" + name }
	if err := st.SetCursor(ctx, legacy("o/old"), fmt.Sprintf("%d|0", stamp)); err != nil {
		t.Fatal(err)
	}
	if !e.RemovedRecently(ctx, "o/old", 0) {
		t.Fatal("a legacy tombstone was not honoured")
	}
	if err := st.UpsertRepo(ctx, model.Repo{FullName: "o/new"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "o/new"); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.GetCursor(ctx, removedCursor+"o/new"); v == "" {
		t.Fatal("a removal left no tombstone")
	}
	if got, _ := st.GetCursor(ctx, "removed|o/new"); got != "" {
		t.Fatalf("a removal wrote the legacy name: %q", got)
	}
	if !e.RemovedRecently(ctx, "o/new", 0) {
		t.Fatal("the new tombstone was not honoured")
	}
	if err := e.ForgetRename(ctx, "o/old"); err != nil {
		t.Fatal(err)
	}
	if e.RemovedRecently(ctx, "o/old", 0) {
		t.Fatal("a re-add did not clear the legacy tombstone")
	}

	// Past the TTL both kinds are swept, and the legacy prefix is looked at no more once a
	// whole TTL has passed since the first sweep.
	now = now.Add(removedTTL + time.Hour)
	if n, err := e.SweepTombstones(ctx); err != nil || n < 2 {
		t.Fatalf("sweep = %d, %v; want the expired new and the cleared legacy tombstone", n, err)
	}
	if v, _ := st.GetCursor(ctx, removedCursor+"o/new"); v != "" {
		t.Fatalf("expired tombstone survived: %q", v)
	}
	if err := st.SetCursor(ctx, legacy("o/late"), "1|0"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(removedTTL + time.Hour)
	if _, err := e.SweepTombstones(ctx); err != nil { // final legacy sweep
		t.Fatal(err)
	}
	if v, _ := st.GetCursor(ctx, legacy("o/late")); v != "" {
		t.Fatalf("legacy tombstone survived the final sweep: %q", v)
	}
	if err := st.SetCursor(ctx, legacy("o/ignored"), "1|0"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SweepTombstones(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.GetCursor(ctx, legacy("o/ignored")); v == "" {
		t.Fatal("the legacy prefix was still being scanned after its last sweep")
	}
}
