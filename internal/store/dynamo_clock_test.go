package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// WithClock moves the handle's lease clock: a handle whose clock is ahead by more than the
// skew takes a live lease from another handle (a takeover, token 2); one that is ahead by
// less does not, and one behind never does.
func TestDynamoWithClockDecidesLeaseTakeover(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, f *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		at := func(off time.Duration) store.DynamoOption {
			return store.WithClock(func() time.Time { return time.Now().Add(off) })
		}
		margin := store.WithLeaseSkew(time.Second)
		holder := newDynamoTable(t, db, table, margin)
		l, ok, err := holder.AcquireLease(ctx, "repo:x", "holder", 30*time.Second)
		if err != nil || !ok || l.Token != 1 {
			t.Fatalf("holder acquire: %+v ok=%v err=%v", l, ok, err)
		}
		for name, off := range map[string]time.Duration{"behind": -time.Minute, "within the skew": 500 * time.Millisecond} {
			other := store.NewDynamo(db, table, margin, at(off))
			if _, ok, err := other.AcquireLease(ctx, "repo:x", name, 30*time.Second); err != nil || ok {
				t.Fatalf("%s: ok=%v err=%v, want the live lease refused", name, ok, err)
			}
		}
		ahead := store.NewDynamo(db, table, margin, at(time.Minute))
		got, ok, err := ahead.AcquireLease(ctx, "repo:x", "ahead", 30*time.Second)
		if err != nil || !ok || got.Token != 2 {
			t.Fatalf("a clock a minute ahead: %+v ok=%v err=%v, want a takeover with token 2", got, ok, err)
		}
	})
}
