package store_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

const hotBase = "PKG#go#example.com/hot"

// counter reads the adaptive counter item of the hot package: its Deps, its Shards (0 when
// unrecorded) and whether the item exists.
func counter(t *testing.T, db *dynamodb.Client, table string) (deps, shards int, ok bool) {
	t.Helper()
	out, err := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{"PK": sv("PKGCOUNT#" + hotBase), "SK": sv("META")}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Item == nil {
		return 0, 0, false
	}
	num := func(k string) int {
		v, present := out.Item[k]
		if !present {
			return 0
		}
		i, err := strconv.Atoi(v.(*types.AttributeValueMemberN).Value)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	return num("Deps"), num("Shards"), true
}

// seedLegacy writes n dependents of the hot package with the option off, the way a table
// looked before adaptive sharding existed: all under the legacy key, no counter.
func seedLegacy(t *testing.T, db *dynamodb.Client, table string, n int) {
	t.Helper()
	legacy := newDynamoTable(t, db, table)
	for i := 0; i < n; i++ {
		r := fmt.Sprintf("acme/r%03d", i)
		if err := legacy.ReplaceDependencies(context.Background(), r, []model.Dependency{dep(r)}); err != nil {
			t.Fatal(err)
		}
	}
}

func listedOnce(t *testing.T, d *store.Dynamo, want int) {
	t.Helper()
	got, err := d.ListDependents(context.Background(), "go", "example.com/hot")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, x := range got {
		if seen[x.Repo] {
			t.Fatalf("%s listed twice", x.Repo)
		}
		seen[x.Repo] = true
	}
	if len(got) != want {
		t.Fatalf("%d dependents listed, want %d", len(got), want)
	}
}

// Dependents that predate the counter are counted, and each moves to the shard its repo
// hashes to, so the package stops sitting in one partition.
func TestDynamoRecountDependentsCountsAndMovesLegacyItems(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 25)
		ad := store.NewDynamo(db, table, store.WithAdaptiveDependents(10))
		if err := ad.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		if got := hotPartitions(t, db, table, 4); len(got) != 1 {
			t.Fatalf("precondition: %v, want one partition", got)
		}

		n, err := ad.RecountDependents(ctx, "go", "example.com/hot")
		if err != nil {
			t.Fatal(err)
		}
		if n != 25 {
			t.Fatalf("RecountDependents = %d, want 25", n)
		}
		// 25 > 10*2 so four shards.
		if deps, shards, ok := counter(t, db, table); !ok || deps != 25 || shards != 4 {
			t.Fatalf("counter = deps %d shards %d present %v, want 25, 4", deps, shards, ok)
		}
		parts := hotPartitions(t, db, table, 4)
		if len(parts) != 4 {
			t.Fatalf("partitions %v, want all four shards occupied", parts)
		}
		total := 0
		for pk, c := range parts {
			total += c
			if c > 15 {
				t.Fatalf("partition %q still holds %d of 25", pk, c)
			}
		}
		if total != 25 {
			t.Fatalf("%d items under the shard keys, want 25", total)
		}
		listedOnce(t, ad, 25)

		// Idempotent: a second recount changes nothing.
		if n, err := ad.RecountDependents(ctx, "go", "example.com/hot"); err != nil || n != 25 {
			t.Fatalf("second recount = %d, %v", n, err)
		}
		if deps, shards, _ := counter(t, db, table); deps != 25 || shards != 4 {
			t.Fatalf("counter after second recount = %d, %d", deps, shards)
		}
		listedOnce(t, ad, 25)
	})
}

// A drifted counter is corrected; the recorded shard count is a high-water mark and never
// shrinks, because items may still sit in the higher shards of a reader that has not caught up.
func TestDynamoRecountDependentsCorrectsDriftButKeepsHighWaterMark(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		ad := newDynamoTable(t, db, table, store.WithAdaptiveDependents(10))
		for i := 0; i < 5; i++ {
			r := fmt.Sprintf("acme/r%03d", i)
			if err := ad.ReplaceDependencies(ctx, r, []model.Dependency{dep(r)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: map[string]types.AttributeValue{
			"PK": sv("PKGCOUNT#" + hotBase), "SK": sv("META"),
			"Deps": &types.AttributeValueMemberN{Value: "900"}, "Shards": &types.AttributeValueMemberN{Value: "8"},
		}}); err != nil {
			t.Fatal(err)
		}
		n, err := ad.RecountDependents(ctx, "go", "example.com/hot")
		if err != nil || n != 5 {
			t.Fatalf("RecountDependents = %d, %v, want 5", n, err)
		}
		if deps, shards, _ := counter(t, db, table); deps != 5 || shards != 8 {
			t.Fatalf("counter = deps %d shards %d, want 5 and the high-water 8 kept", deps, shards)
		}
		listedOnce(t, ad, 5)
	})
}

// With no counter, the first write that would adjust it recounts the package instead of
// starting from zero, so dependents that predate the option are not lost to the count.
func TestDynamoLazyRecountWhenCounterIsMissing(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 25)
		ad := store.NewDynamo(db, table, store.WithAdaptiveDependents(10))
		if err := ad.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, ok := counter(t, db, table); ok {
			t.Fatal("precondition: counter exists")
		}
		if err := ad.ReplaceDependencies(ctx, "acme/new", []model.Dependency{dep("acme/new")}); err != nil {
			t.Fatal(err)
		}
		if deps, shards, ok := counter(t, db, table); !ok || deps != 26 || shards != 4 {
			t.Fatalf("counter = deps %d shards %d present %v, want 26, 4", deps, shards, ok)
		}
		listedOnce(t, ad, 26)
		// The counter now exists, so the next change is a plain ADD.
		if err := ad.DeleteRepo(ctx, "acme/new"); err != nil {
			t.Fatal(err)
		}
		if deps, _, _ := counter(t, db, table); deps != 25 {
			t.Fatalf("counter after DeleteRepo = %d, want 25", deps)
		}
		listedOnce(t, ad, 25)
	})
}

// RecountDependents is for the adaptive option; without it there is no counter to fix.
func TestDynamoRecountDependentsNeedsTheAdaptiveOption(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		d := newDynamoTable(t, db, "ripple_test_"+suffix())
		if _, err := d.RecountDependents(context.Background(), "go", "example.com/hot"); err == nil {
			t.Fatal("RecountDependents succeeded with adaptive dependents off")
		}
	})
}
