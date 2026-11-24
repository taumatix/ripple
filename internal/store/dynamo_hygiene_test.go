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

// A table that has had the option on keeps its counters moving when it is switched off, so
// turning it back on later starts from a count that is still true.
func TestDynamoCountersFollowWritesWithTheOptionOff(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		on := newDynamoTable(t, db, table, store.WithAdaptiveDependents(100))
		addHot(t, on, 0, 12)
		off := newDynamoTable(t, db, table)
		addHot(t, off, 12, 15)
		if n, _, ok := counter(t, db, table); !ok || n != 15 {
			t.Fatalf("counter = %d (exists %v) after 3 writes with the option off, want 15", n, ok)
		}
		if err := off.DeleteRepo(ctx, "acme/r000"); err != nil {
			t.Fatal(err)
		}
		if n, shards, _ := counter(t, db, table); n != 14 || shards > 1 {
			t.Fatalf("counter = %d, shards %d after a delete with the option off, want 14 and no growth", n, shards)
		}
	})
}

// A table that never had the option on keeps no counters at all.
func TestDynamoNoCountersWhenTheOptionWasNeverOn(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 3)
		if _, _, ok := counter(t, db, table); ok {
			t.Fatal("a counter was written on a table that never had adaptive dependents")
		}
	})
}

func coldDep(repo string) model.Dependency {
	d := dep(repo)
	d.Package = "example.com/cold"
	return d
}

// CheckDependentCounts reports every package whose counter disagrees with its index, and
// RecountAllDependents repairs them all, including packages that have no counter yet.
func TestDynamoRecountAllDependents(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 12)
		legacy := newDynamoTable(t, db, table)
		if err := legacy.ReplaceDependencies(ctx, "acme/c1", []model.Dependency{coldDep("acme/c1")}); err != nil {
			t.Fatal(err)
		}
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(5))
		drift, err := d.CheckDependentCounts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]store.CountDrift{}
		for _, x := range drift {
			got[x.Base] = x
		}
		if len(got) != 2 || got[hotBase].Indexed != 12 || got[hotBase].Counted != 0 ||
			got["PKG#go#example.com/cold"].Indexed != 1 {
			t.Fatalf("drift = %+v, want hot 12 vs 0 and cold 1 vs 0", drift)
		}
		if _, _, ok := counter(t, db, table); ok {
			t.Fatal("the check wrote a counter")
		}
		n, err := d.RecountAllDependents(ctx)
		if err != nil || n != 2 {
			t.Fatalf("RecountAllDependents = %d, %v, want 2 packages", n, err)
		}
		if deps, shards, _ := counter(t, db, table); deps != 12 || shards < 2 {
			t.Fatalf("counter = %d, shards %d, want 12 and grown", deps, shards)
		}
		listedOnce(t, d, 12)
		if drift, err = d.CheckDependentCounts(ctx); err != nil || len(drift) != 0 {
			t.Fatalf("drift after a recount = %+v, %v, want none", drift, err)
		}
		// A counter that drifted is found and repaired.
		if _, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(table),
			Key:                       map[string]types.AttributeValue{"PK": sv("PKGCOUNT#" + hotBase), "SK": sv("META")},
			UpdateExpression:          aws.String("SET Deps = :d"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":d": &types.AttributeValueMemberN{Value: "99"}}}); err != nil {
			t.Fatal(err)
		}
		if drift, _ = d.CheckDependentCounts(ctx); len(drift) != 1 || drift[0].Counted != 99 {
			t.Fatalf("drift = %+v, want hot counted 99", drift)
		}
	})
}

func TestDynamoRecountAllNeedsTheOption(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		d := newDynamoTable(t, db, "ripple_test_"+suffix())
		if _, err := d.RecountAllDependents(context.Background()); err == nil {
			t.Fatal("RecountAllDependents succeeded without WithAdaptiveDependents")
		}
	})
}

// Failed jobs are not in the pending index, so the open-job backfill never sees them;
// BackfillFailedJobs finds them by scanning and writes their missing copies.
func TestDynamoBackfillFailedJobs(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		failed := []model.Job{repoJob("acme/web", 1, model.JobFailed), repoJob("acme/web", 2, model.JobFailed), repoJob("acme/api", 3, model.JobFailed)}
		done := repoJob("acme/web", 4, model.JobDone)
		for _, j := range append([]model.Job{done}, failed...) {
			if _, err := d.EnqueueJob(ctx, j); err != nil {
				t.Fatal(err)
			}
		}
		// A table from before the copies existed: the failed jobs have none.
		for _, j := range failed {
			if _, err := db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(table), Key: openCopyKey(j)}); err != nil {
				t.Fatal(err)
			}
		}
		if got := openPackages(t, d, "acme/web"); len(got) != 0 {
			t.Fatalf("open jobs before the backfill = %v", got)
		}
		n, err := d.BackfillFailedJobs(ctx)
		if err != nil || n != 3 {
			t.Fatalf("BackfillFailedJobs = %d, %v, want 3", n, err)
		}
		if got := openPackages(t, d, "acme/web"); len(got) != 2 {
			t.Fatalf("acme/web open jobs = %v, want its 2 failed jobs", got)
		}
		if got := openPackages(t, d, "acme/api"); len(got) != 1 {
			t.Fatalf("acme/api open jobs = %v, want 1", got)
		}
		if hasOpenCopy(t, db, table, done) {
			t.Fatal("a done job got a copy")
		}
		if n, err := d.BackfillFailedJobs(ctx); err != nil || n != 0 {
			t.Fatalf("second BackfillFailedJobs = %d, %v, want 0", n, err)
		}
	})
}

// With paging on, the commands read the table a page at a time with a pause between pages
// and find exactly what they find unpaced; a cancelled context ends the wait.
func TestDynamoHygieneScansArePaced(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, fake *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 12)
		const pause = 40 * time.Millisecond
		paced := newDynamoTable(t, db, table, store.WithScanPaging(5, pause))
		// The scan that builds the package index is paced too, and a cancelled one leaves no
		// marker, so the next check scans again.
		cctx, cancel := context.WithTimeout(ctx, pause/2)
		defer cancel()
		if _, err := paced.CheckDependentCounts(cctx); err == nil {
			t.Fatal("a cancelled context did not end a paced scan")
		}
		var before int
		if fake != nil {
			before = fake.Calls("Scan")
		}
		start := time.Now()
		drift, err := paced.CheckDependentCounts(ctx)
		took := time.Since(start)
		if err != nil || len(drift) != 1 || drift[0].Indexed != 12 {
			t.Fatalf("paced check = %+v, %v, want the one package with 12 indexed", drift, err)
		}
		if took < 2*pause { // 12 dependents and their index entries span at least three pages of 5
			t.Fatalf("paced check took %s, want at least %s", took, 2*pause)
		}
		if fake != nil {
			if pages := fake.Calls("Scan") - before; pages < 3 {
				t.Fatalf("scan pages = %d, want at least 3 of 5 items", pages)
			}
		}
	})
}
