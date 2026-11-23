package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func sv(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func dep(repo string) model.Dependency {
	return model.Dependency{Repo: repo, File: "go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/hot", Version: "v1.0.0"}
}

func repoNames(ds []model.Dependency) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Repo)
	}
	return out
}

// hotPartitions lists every distinct GSI1PK that holds a dependent of the hot package.
func hotPartitions(t *testing.T, db *dynamodb.Client, table string, shards int) map[string]int {
	t.Helper()
	out := map[string]int{}
	base := "PKG#go#example.com/hot"
	for sh := 0; sh < 64 && sh < shards*2; sh++ {
		pk := base
		if sh > 0 {
			pk = fmt.Sprintf("%s\x1f%d", base, sh)
		}
		if c := gsiCount(t, db, table, pk); c > 0 {
			out[pk] = c
		}
	}
	return out
}

// The migration path: a table written with one partition per package, then adaptive sharding
// switched on, grows a hot package's shard count as dependents arrive; every read between
// returns each dependent exactly once; switching the option off again loses nothing.
func TestDynamoAdaptiveDependentsMigration(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		const perShard = 10

		legacy := newDynamoTable(t, db, table) // before the feature: one partition per package
		var want []string
		for i := 0; i < 8; i++ {
			r := fmt.Sprintf("acme/r%03d", i)
			want = append(want, r)
			if err := legacy.ReplaceDependencies(ctx, r, []model.Dependency{dep(r)}); err != nil {
				t.Fatal(err)
			}
		}

		ad := store.NewDynamo(db, table, store.WithAdaptiveDependents(perShard))
		if err := ad.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		check := func(s *store.Dynamo, when string) {
			t.Helper()
			got, err := s.ListDependents(ctx, "go", "example.com/hot")
			if err != nil {
				t.Fatal(err)
			}
			if names := repoNames(got); len(names) != len(want) {
				t.Fatalf("%s: %d dependents, want %d (%v)", when, len(names), len(want), names)
			}
			seen := map[string]bool{}
			for _, d := range got {
				if seen[d.Repo] {
					t.Fatalf("%s: %s listed twice", when, d.Repo)
				}
				seen[d.Repo] = true
			}
			for _, r := range want {
				if !seen[r] {
					t.Fatalf("%s: %s lost", when, r)
				}
			}
		}
		check(ad, "legacy items read by an adaptive store")

		grown := 0
		for i := 8; i < 70; i++ {
			r := fmt.Sprintf("acme/r%03d", i)
			want = append(want, r)
			if err := ad.ReplaceDependencies(ctx, r, []model.Dependency{dep(r)}); err != nil {
				t.Fatal(err)
			}
			if i%9 == 0 {
				check(ad, fmt.Sprintf("after %d dependents", len(want)))
			}
			if n := len(hotPartitions(t, db, table, 16)); n > grown {
				grown = n
			}
		}
		check(ad, "after growth")
		// 69 dependents at 10 per shard: 1 -> 2 -> 4 -> 8 (69 > 10*4), so items live in several partitions.
		if grown < 4 {
			t.Fatalf("hot package occupies %d partitions after 70 dependents at %d per shard, want at least 4", grown, perShard)
		}
		if got := gsiCount(t, db, table, "PKG#go#example.com/hot"); got == 0 {
			t.Fatal("shard 0 no longer holds anything under the legacy key")
		}

		// Option switched off: the flag recorded in the table keeps every shard readable.
		off := store.NewDynamo(db, table)
		if err := off.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		check(off, "adaptive off after having been on")
		// ... and writes from the off store land where reads will look.
		want = append(want, "acme/late")
		if err := off.ReplaceDependencies(ctx, "acme/late", []model.Dependency{dep("acme/late")}); err != nil {
			t.Fatal(err)
		}
		check(off, "write by an adaptive-off store")
		check(ad, "other instance sees it too")

		// Removing a repo drops it from the dependents.
		if err := ad.DeleteRepo(ctx, "acme/late"); err != nil {
			t.Fatal(err)
		}
		want = want[:len(want)-1]
		check(ad, "after DeleteRepo")
	})
}

// A package that stays small pays nothing: still one partition, still the legacy key.
func TestDynamoAdaptiveLeavesSmallPackagesAlone(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(50))
		for i := 0; i < 20; i++ {
			r := fmt.Sprintf("acme/r%02d", i)
			if err := d.ReplaceDependencies(ctx, r, []model.Dependency{dep(r)}); err != nil {
				t.Fatal(err)
			}
		}
		parts := hotPartitions(t, db, table, 8)
		if len(parts) != 1 || parts["PKG#go#example.com/hot"] != 20 {
			t.Fatalf("partitions %v, want all 20 under the legacy key", parts)
		}
	})
}

// Replacing a repo's dependencies with the same set must not inflate the counter, or a
// package would grow shards just from being re-scanned.
func TestDynamoAdaptiveRescanDoesNotInflateCount(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(3))
		for i := 0; i < 2; i++ {
			if err := d.ReplaceDependencies(ctx, "acme/a", []model.Dependency{dep("acme/a")}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 10; i++ {
			if err := d.ReplaceDependencies(ctx, "acme/b", []model.Dependency{dep("acme/b")}); err != nil {
				t.Fatal(err)
			}
		}
		out, err := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
			Key: map[string]types.AttributeValue{"PK": sv("PKGCOUNT#PKG#go#example.com/hot"), "SK": sv("META")}})
		if err != nil {
			t.Fatal(err)
		}
		if got := out.Item["Deps"].(*types.AttributeValueMemberN).Value; got != "2" {
			t.Fatalf("counter = %s after 2 repos rescanned, want 2", got)
		}
		if _, grown := out.Item["Shards"]; grown {
			t.Fatal("two dependents at 3 per shard must not add shards")
		}
	})
}

// Concurrent and sequential reads must return the same thing in the same order, and the
// concurrent one must respect its bound.
func TestDynamoConcurrentReadsMatchSequential(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, fake *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seq := newDynamoTable(t, db, table, store.WithJobShards(8), store.WithDependentShards(8), store.WithQueryConcurrency(1))
		par := store.NewDynamo(db, table, store.WithJobShards(8), store.WithDependentShards(8), store.WithQueryConcurrency(3))
		if err := par.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		for i := 0; i < 120; i++ {
			// Equal NotBefore for many jobs so the tie-break by key is what orders them.
			j := pendingJob(i, now.Add(-time.Duration(i%4)*time.Minute))
			if _, err := seq.EnqueueJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			r := fmt.Sprintf("acme/r%03d", i)
			if err := seq.ReplaceDependencies(ctx, r, []model.Dependency{dep(r)}); err != nil {
				t.Fatal(err)
			}
		}
		if fake != nil {
			fake.SetQueryDelay(15 * time.Millisecond)
			fake.ResetQueryStats()
		}
		wantJobs, err := seq.ListDueJobs(ctx, now, 50)
		if err != nil {
			t.Fatal(err)
		}
		wantDeps, err := seq.ListDependents(ctx, "go", "example.com/hot")
		if err != nil {
			t.Fatal(err)
		}
		if fake != nil {
			if _, peak := fake.QueryStats(); peak != 1 {
				t.Fatalf("sequential store ran %d queries at once", peak)
			}
			fake.ResetQueryStats()
		}
		for i := 0; i < 3; i++ {
			gotJobs, err := par.ListDueJobs(ctx, now, 50)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotJobs, wantJobs) {
				t.Fatalf("concurrent due jobs differ from sequential:\n got %v\nwant %v", jobKeys(gotJobs), jobKeys(wantJobs))
			}
			gotDeps, err := par.ListDependents(ctx, "go", "example.com/hot")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotDeps, wantDeps) || len(gotDeps) != 120 {
				t.Fatalf("concurrent dependents differ from sequential (%d vs %d)", len(gotDeps), len(wantDeps))
			}
		}
		if fake != nil {
			if _, peak := fake.QueryStats(); peak < 2 || peak > 3 {
				t.Fatalf("peak concurrent queries %d, want 2..3 (bound 3)", peak)
			}
		}
	})
}

func jobKeys(js []model.Job) []string {
	var out []string
	for _, j := range js {
		out = append(out, j.Key)
	}
	return out
}

// A cancelled context returns promptly instead of finishing every shard's Query.
func TestDynamoConcurrentReadHonoursCancel(t *testing.T) {
	f := dynamofake.New()
	t.Cleanup(f.Close)
	db := dynamoSDK(t, f.URL())
	d := newDynamoTable(t, db, "ripple_test_"+suffix(), store.WithJobShards(16), store.WithQueryConcurrency(4))
	f.SetQueryDelay(300 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.ListDueJobs(ctx, time.Now(), 10)
	if err == nil {
		t.Fatal("want an error from the cancelled context")
	}
	if el := time.Since(start); el > 250*time.Millisecond {
		t.Fatalf("returned after %v; 16 shards of 300ms at concurrency 4 would take 1.2s, and cancel should cut it to well under one query", el)
	}
}

// BatchWriteItem leaving items unprocessed is retried until they land.
func TestDynamoBatchRetriesUnprocessedItems(t *testing.T) {
	f := dynamofake.New()
	t.Cleanup(f.Close)
	db := dynamoSDK(t, f.URL())
	ctx := context.Background()
	d := newDynamoTable(t, db, "ripple_test_"+suffix())
	f.DropOneFromNextBatch()
	var deps []model.Dependency
	for i := 0; i < 5; i++ {
		deps = append(deps, model.Dependency{Repo: "acme/x", File: fmt.Sprintf("f%d/go.mod", i), Manager: "gomod", Datasource: "go", Package: "example.com/hot", Version: "v1"})
	}
	if err := d.ReplaceDependencies(ctx, "acme/x", deps); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListRepoDependencies(ctx, "acme/x")
	if err != nil || len(got) != 5 {
		t.Fatalf("%d of 5 dependencies written after an unprocessed batch: %v", len(got), err)
	}
}
