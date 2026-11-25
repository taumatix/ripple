package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// A host whose listing outgrows DynamoDB's 400 KB item limit is still tracked: the listing is
// kept one cursor per repo, so the diff of the next listing is read back and acted on.
func TestDiscoverTracksAListingLargerThanOneDynamoItem(t *testing.T) {
	ctx := context.Background()
	f := dynamofake.New()
	t.Cleanup(f.Close)
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
	if err != nil {
		t.Fatal(err)
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(f.URL()) })
	st := store.NewDynamo(db, "discover")
	if err := st.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	lh := &listingHost{}
	a := &App{Hosts: []*platform.Host{{Name: "gerrit", Platform: lh}}, Queue: &Queue{Store: st, Process: newGate().process}}

	// 15000 names of about 38 bytes is a 570 KB JSON list; each step adds a few thousand, so
	// no single event is near the limit either.
	var names []string
	for step := 0; step < 5; step++ {
		for i := 0; i < 3000; i++ {
			names = append(names, fmt.Sprintf("org/group-%02d/project-%05d", step, i))
		}
		lh.set(names...)
		a.Discover(ctx)
		drain(t, st)
	}

	lh.set(names[1:]...)
	a.Discover(ctx)
	if got, want := fmt.Sprint(queued(t, st)), "[repos_removed gerrit:"+names[0]+"]"; got != want {
		t.Fatalf("after one repo left a 15000-repo listing, queued %s, want %s", got, want)
	}
}

// A listing stored by an earlier version as one JSON cursor is read, moved to per-repo keys and
// deleted, with no event for the repos it already held.
func TestDiscoverMovesALegacyListingToPerRepoKeys(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.SetCursor(ctx, "discover/gerrit", `["a","b"]`); err != nil {
		t.Fatal(err)
	}
	lh := &listingHost{}
	a := &App{Hosts: []*platform.Host{{Name: "gerrit", Platform: lh}}, Queue: &Queue{Store: st, Process: newGate().process}}
	lh.set("a", "b")
	a.Discover(ctx)
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("a listing already known queued %v", got)
	}
	if v, _ := st.GetCursor(ctx, "discover/gerrit"); v != "" {
		t.Errorf("legacy cursor kept: %q", v)
	}
	got, _ := st.ListCursors(ctx, "discover/gerrit#")
	if len(got) != 2 {
		t.Fatalf("per-repo keys = %v, want a and b", got)
	}
	lh.set("b", "c")
	a.Discover(ctx)
	if got := fmt.Sprint(queued(t, st)); got != "[repos_added gerrit:c repos_removed gerrit:a]" {
		t.Fatalf("queued %s", got)
	}
}
