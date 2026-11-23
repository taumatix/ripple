package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func olderEv(id string, seq int64, repo string) model.Event {
	return model.Event{ID: id, Seq: seq, InstallationID: 7, Kind: "push", Repo: repo}
}

// HasOlderEvent reads the base table, so an older event written a moment ago is seen while
// the index still lags, and a dead-lettered or deleted one stops counting at once.
func TestDynamoHasOlderEventIsConsistent(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, f *dynamofake.Fake) {
		ctx := context.Background()
		d := newDynamoTable(t, db, "ripple_test_"+suffix(), store.WithLeaseSkew(0))
		newer := olderEv("n", 20, "acme/web")
		older := func(want bool, why string) {
			t.Helper()
			got, err := d.HasOlderEvent(ctx, newer)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("HasOlderEvent = %v, want %v: %s", got, want, why)
			}
		}
		older(false, "an empty queue") // also builds the markers of events from before
		if f != nil {
			f.HoldIndex()
			defer f.ReleaseIndex()
		}
		if _, err := d.EnqueueEvent(ctx, olderEv("a", 10, "acme/web")); err != nil {
			t.Fatal(err)
		}
		older(true, "EnqueueEvent, index lagging")
		dead := olderEv("a", 10, "acme/web")
		dead.DeadAt = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		if err := d.UpdateEvent(ctx, dead); err != nil {
			t.Fatal(err)
		}
		older(false, "dead-lettered")
		if ok, err := d.RequeueDeadEvent(ctx, "a"); err != nil || !ok {
			t.Fatalf("requeue: %v %v", ok, err)
		}
		older(true, "requeued")
		if err := d.DeleteEvent(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		older(false, "deleted")

		seq, ok, err := d.EnqueueSequenced(ctx, model.Event{ID: "s", InstallationID: 7, Kind: "push", Repo: "acme/web"})
		if err != nil || !ok {
			t.Fatalf("sequenced: %v %v", ok, err)
		}
		if got, _ := d.HasOlderEvent(ctx, olderEv("later", seq.Seq+1, "acme/web")); !got {
			t.Fatal("an event numbered by the store is not seen, index lagging")
		}
		if got, _ := d.HasOlderEvent(ctx, olderEv("later", seq.Seq+1, "acme/api")); got {
			t.Fatal("another repo's event counted")
		}

		l, got, err := d.AcquireLease(ctx, "feed", "me", time.Minute)
		if err != nil || !got {
			t.Fatalf("lease: %v %v", got, err)
		}
		fdead := seq
		fdead.DeadAt = dead.DeadAt
		if err := d.UpdateEventFenced(ctx, fdead, l); err != nil {
			t.Fatal(err)
		}
		older(false, "dead-lettered under the lease")
		if err := d.DeleteEventFenced(ctx, "s", l); err != nil {
			t.Fatal(err)
		}
		if _, err := d.EnqueueEvent(ctx, olderEv("b", 11, "acme/web")); err != nil {
			t.Fatal(err)
		}
		if err := d.DeleteEventFenced(ctx, "b", l); err != nil {
			t.Fatal(err)
		}
		older(false, "deleted under the lease")
	})
}

// Events written before the markers existed are found by the first check, and a marker whose
// event an older replica deleted without removing it does not hold anything back.
func TestDynamoHasOlderEventBackfillsAndHealsStaleMarkers(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithLeaseSkew(0))
		// An event as the previous version wrote it: the item and its index keys, no marker.
		legacy := map[string]types.AttributeValue{"PK": sv("EVENT#old"), "SK": sv("META"), "GSI1PK": sv("EVENTS"),
			"GSI1SK": sv("00000000000000000010\x1fold"), "ID": sv("old"), "Kind": sv("push"), "Repo": sv("acme/web"),
			"Seq": &types.AttributeValueMemberN{Value: "10"}, "InstallationID": &types.AttributeValueMemberN{Value: "7"},
			"NotBefore": sv("0001-01-01T00:00:00Z")}
		if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: legacy}); err != nil {
			t.Fatal(err)
		}
		if got, err := d.HasOlderEvent(ctx, olderEv("n", 20, "acme/web")); err != nil || !got {
			t.Fatalf("a pre-marker event: %v %v", got, err)
		}
		// An older replica deletes the event and leaves its marker behind.
		if _, err := db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(table),
			Key: map[string]types.AttributeValue{"PK": sv("EVENT#old"), "SK": sv("META")}}); err != nil {
			t.Fatal(err)
		}
		if got, err := d.HasOlderEvent(ctx, olderEv("n", 20, "acme/web")); err != nil || got {
			t.Fatalf("a stale marker held an event back: %v %v", got, err)
		}
	})
}
