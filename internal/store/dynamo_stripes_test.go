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

// counterItems returns Deps of every item in the hot package's counter partition, by sort
// key: META for the unstriped counter and S#<i> for each stripe.
func counterItems(t *testing.T, db *dynamodb.Client, table string) (map[string]int, int) {
	t.Helper()
	out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": sv("PKGCOUNT#" + hotBase)}})
	if err != nil {
		t.Fatal(err)
	}
	by, sum := map[string]int{}, 0
	for _, it := range out.Items {
		sk := it["SK"].(*types.AttributeValueMemberS).Value
		d := 0
		if v, ok := it["Deps"].(*types.AttributeValueMemberN); ok {
			d, _ = strconv.Atoi(v.Value)
		}
		by[sk] = d
		sum += d
	}
	return by, sum
}

func addHot(t *testing.T, d *store.Dynamo, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		r := fmt.Sprintf("acme/r%03d", i)
		if err := d.ReplaceDependencies(context.Background(), r, []model.Dependency{dep(r)}); err != nil {
			t.Fatal(err)
		}
	}
}

// Striping spreads the counter's writes over several items; the dependents count is their
// sum, and the package still grows shards from it and reads every dependent once.
func TestDynamoCounterStripesSpreadWritesAndStillGrowShards(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(10), store.WithCounterStripes(4))
		addHot(t, d, 0, 60)
		by, sum := counterItems(t, db, table)
		if sum != 60 {
			t.Fatalf("counter items %v sum to %d, want 60", by, sum)
		}
		stripes := 0
		for sk := range by {
			if len(sk) > 2 && sk[:2] == "S#" {
				stripes++
			}
		}
		if stripes < 2 || stripes > 4 {
			t.Fatalf("%d stripe items in %v, want between 2 and 4", stripes, by)
		}
		if _, shards, _ := counter(t, db, table); shards < 4 {
			t.Fatalf("recorded shards = %d after 60 dependents at 10 per shard, want at least 4", shards)
		}
		listedOnce(t, d, 60)

		// Removing dependents moves the same sum down.
		for i := 0; i < 10; i++ {
			if err := d.DeleteRepo(ctx, fmt.Sprintf("acme/r%03d", i)); err != nil {
				t.Fatal(err)
			}
		}
		if _, sum := counterItems(t, db, table); sum != 50 {
			t.Fatalf("sum after 10 deletes = %d, want 50", sum)
		}
		listedOnce(t, d, 50)
	})
}

// A counter written before striping (one META item) keeps counting: stripes add to it
// rather than replacing it, and a stripe's first write does not mistake it for a missing counter.
func TestDynamoCounterStripesAddToAnUnstripedCounter(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		table := "ripple_test_" + suffix()
		plain := newDynamoTable(t, db, table, store.WithAdaptiveDependents(100))
		addHot(t, plain, 0, 15)
		striped := store.NewDynamo(db, table, store.WithAdaptiveDependents(100), store.WithCounterStripes(4))
		addHot(t, striped, 15, 25)
		by, sum := counterItems(t, db, table)
		if sum != 25 || by["META"] != 15 {
			t.Fatalf("counter items %v (sum %d), want META still 15 and a sum of 25", by, sum)
		}
		listedOnce(t, striped, 25)
	})
}

// A recount folds stripes back: the truth lands in META and the stripes are zeroed, so the
// sum is exact whatever drifted.
func TestDynamoRecountDependentsResetsStripes(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(100), store.WithCounterStripes(4))
		addHot(t, d, 0, 12)
		if _, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(table),
			Key:                       map[string]types.AttributeValue{"PK": sv("PKGCOUNT#" + hotBase), "SK": sv("S#0")},
			UpdateExpression:          aws.String("ADD Deps :d"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":d": &types.AttributeValueMemberN{Value: "500"}}}); err != nil {
			t.Fatal(err)
		}
		if n, err := d.RecountDependents(ctx, "go", "example.com/hot"); err != nil || n != 12 {
			t.Fatalf("RecountDependents = %d, %v, want 12", n, err)
		}
		by, sum := counterItems(t, db, table)
		if sum != 12 || by["META"] != 12 {
			t.Fatalf("counter items %v (sum %d), want META 12 and stripes zero", by, sum)
		}
	})
}
