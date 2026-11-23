package store_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func openCopyKey(j model.Job) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": sv("REPO#" + j.Repo), "SK": sv("OPENJOB#" + j.Key)}
}

func hasOpenCopy(t *testing.T, db *dynamodb.Client, table string, j model.Job) bool {
	t.Helper()
	out, err := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: openCopyKey(j)})
	if err != nil {
		t.Fatal(err)
	}
	return out.Item != nil
}

func openPackages(t *testing.T, d *store.Dynamo, repo string) []string {
	t.Helper()
	got, err := d.ListOpenJobs(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, j := range got {
		out = append(out, j.Key)
	}
	sort.Strings(out)
	return out
}

func repoJob(repo string, i int, state model.JobState) model.Job {
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	pkg := fmt.Sprintf("example.com/p%03d", i)
	return model.Job{Key: model.JobKey(repo, "go", pkg, "v1.0.0"), Repo: repo, Datasource: "go", Package: pkg, Version: "v1.0.0",
		ReleasedAt: at, State: state, NotBefore: at}
}

// A table written before the copies existed: its pending jobs are in the index but have no
// copy. Opening it with this version backfills them from the index, and it leaves alone a
// copy that is already there (a newer write must not be overwritten with an older view).
func TestDynamoBackfillsOpenJobCopiesFromTheJobIndex(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithJobShards(4))
		var jobs []model.Job
		for i := 0; i < 12; i++ {
			j := repoJob("acme/web", i, model.JobPending)
			jobs = append(jobs, j)
			if _, err := d.EnqueueJob(ctx, j); err != nil {
				t.Fatal(err)
			}
		}
		other := repoJob("acme/api", 0, model.JobPending)
		if _, err := d.EnqueueJob(ctx, other); err != nil {
			t.Fatal(err)
		}
		// What an older ripple left: every copy gone except the first job's.
		for _, j := range append(jobs[1:], other) {
			if _, err := db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(table), Key: openCopyKey(j)}); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(openPackages(t, d, "acme/web")); got != 1 {
			t.Fatalf("before the backfill %d open jobs are listed, want only the kept copy", got)
		}

		d2 := store.NewDynamo(db, table, store.WithJobShards(4))
		if err := d2.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(openPackages(t, d2, "acme/web")); got != 12 {
			t.Fatalf("after the backfill %d open jobs of acme/web are listed, want 12", got)
		}
		if got := len(openPackages(t, d2, "acme/api")); got != 1 {
			t.Fatalf("after the backfill %d open jobs of acme/api are listed, want 1", got)
		}

		// Idempotent: a second start changes nothing and does not duplicate.
		if err := d2.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(openPackages(t, d2, "acme/web")); got != 12 {
			t.Fatalf("after a second start %d open jobs, want 12", got)
		}
	})
}

// The backfill must not clobber a newer copy with the index's older view of the job.
func TestDynamoBackfillKeepsAnExistingCopy(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		j := repoJob("acme/web", 1, model.JobPending)
		if _, err := d.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		// The copy says failed (a newer write); the index entry still says pending.
		c := j
		c.State, c.Attempts, c.Reason = model.JobFailed, 3, "newer"
		m := map[string]types.AttributeValue{"PK": sv("REPO#" + j.Repo), "SK": sv("OPENJOB#" + j.Key), "Key": sv(j.Key), "Repo": sv(j.Repo),
			"State": sv(string(c.State)), "Attempts": &types.AttributeValueMemberN{Value: "3"}, "Reason": sv("newer")}
		if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: m}); err != nil {
			t.Fatal(err)
		}
		if err := store.NewDynamo(db, table).EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		out, err := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: openCopyKey(j)})
		if err != nil {
			t.Fatal(err)
		}
		if got := out.Item["Reason"].(*types.AttributeValueMemberS).Value; got != "newer" {
			t.Fatalf("the backfill overwrote an existing copy (reason %q)", got)
		}
	})
}

// A crash between a job write and its copy write leaves a stale copy. Reading reconciles it
// against the job itself: a finished job's copy is dropped and removed, and a changed job is
// reported as the job is, not as the copy was.
func TestDynamoListOpenJobsReconcilesStaleCopies(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, fake *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		const n = 130 // more than one BatchGetItem (100 keys)
		var jobs []model.Job
		for i := 0; i < n; i++ {
			j := repoJob("acme/web", i, model.JobPending)
			jobs = append(jobs, j)
			if _, err := d.EnqueueJob(ctx, j); err != nil {
				t.Fatal(err)
			}
		}
		// Crash simulation: change the job items directly, as UpdateJob's first write would.
		rewrite := func(j model.Job, edit func(it map[string]types.AttributeValue)) {
			out, err := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
				Key: map[string]types.AttributeValue{"PK": sv("JOB#" + j.Key), "SK": sv("META")}})
			if err != nil || out.Item == nil {
				t.Fatalf("job item: %v", err)
			}
			edit(out.Item)
			if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: out.Item}); err != nil {
				t.Fatal(err)
			}
		}
		done, failed := jobs[3], jobs[120]
		rewrite(done, func(it map[string]types.AttributeValue) {
			it["State"] = sv("done")
			delete(it, "GSI1PK")
			delete(it, "GSI1SK")
		})
		rewrite(failed, func(it map[string]types.AttributeValue) {
			it["State"] = sv("failed")
			it["Reason"] = sv("boom")
			delete(it, "GSI1PK")
			delete(it, "GSI1SK")
		})
		if fake != nil {
			fake.DropOneFromNextBatchGet() // unprocessed keys must be retried, not lost
		}
		got, err := d.ListOpenJobs(ctx, "acme/web")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n-1 {
			t.Fatalf("%d open jobs, want %d (the finished one is gone)", len(got), n-1)
		}
		for _, j := range got {
			if j.Key == done.Key {
				t.Fatal("a finished job is still listed")
			}
			if j.Key == failed.Key && (j.State != model.JobFailed || j.Reason != "boom") {
				t.Fatalf("the changed job is reported as its copy was: %+v", j)
			}
		}
		if hasOpenCopy(t, db, table, done) {
			t.Fatal("the stale copy of the finished job was not removed")
		}
		if !hasOpenCopy(t, db, table, failed) {
			t.Fatal("a still-open job lost its copy")
		}
		// The copy was repaired too: it now says failed.
		out, _ := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: openCopyKey(failed)})
		if s := out.Item["State"].(*types.AttributeValueMemberS).Value; s != "failed" {
			t.Fatalf("copy state %q after reconcile, want failed", s)
		}
	})
}

// A job item that cannot be found (its copy outlived it) is dropped from the listing.
func TestDynamoListOpenJobsDropsACopyWithNoJob(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		j := repoJob("acme/web", 1, model.JobPending)
		m := map[string]types.AttributeValue{"PK": sv("REPO#acme/web"), "SK": sv("OPENJOB#" + j.Key), "Key": sv(j.Key), "Repo": sv(j.Repo), "State": sv("pending")}
		if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: m}); err != nil {
			t.Fatal(err)
		}
		if got := openPackages(t, d, "acme/web"); len(got) != 0 {
			t.Fatalf("an orphan copy is listed: %v", got)
		}
		if hasOpenCopy(t, db, table, j) {
			t.Fatal("the orphan copy was not removed")
		}
	})
}
