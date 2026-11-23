package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
	"github.com/taumatix/ripple/internal/store/storetest"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestMemory(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return store.NewMemory() })
}

func suffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func TestPostgres(t *testing.T) {
	dsn := os.Getenv("RIPPLE_TEST_POSTGRES_DSN")
	if dsn == "" {
		testutil.SkipUnlessRequired(t, "RIPPLE_TEST_POSTGRES_DSN not set; skipping Postgres store tests (`make test-services` starts one)")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		ctx := context.Background()
		s, err := store.NewPostgres(ctx, dsn, "t_"+suffix())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

func TestPostgresMigrationsIdempotent(t *testing.T) {
	dsn := os.Getenv("RIPPLE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("RIPPLE_TEST_POSTGRES_DSN not set")
	}
	schema := "t_" + suffix()
	for i := 0; i < 2; i++ {
		s, err := store.NewPostgres(context.Background(), dsn, schema)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		s.Close()
	}
}

func dynamoSDK(t *testing.T, endpoint string) *dynamodb.Client {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// eachDynamo runs fn against the in-process fake (always) and against DynamoDB Local
// (RIPPLE_TEST_DYNAMODB_ENDPOINT; CI sets it and fails on a skip). The fake is for fast local
// feedback; DynamoDB Local is the source of truth. The *Fake is nil for the real service.
func eachDynamo(t *testing.T, fn func(t *testing.T, db *dynamodb.Client, fake *dynamofake.Fake)) {
	t.Helper()
	t.Run("fake", func(t *testing.T) {
		f := dynamofake.New()
		t.Cleanup(f.Close)
		fn(t, dynamoSDK(t, f.URL()), f)
	})
	t.Run("dynamodb-local", func(t *testing.T) {
		ep := os.Getenv("RIPPLE_TEST_DYNAMODB_ENDPOINT")
		if ep == "" {
			testutil.SkipUnlessRequired(t, "RIPPLE_TEST_DYNAMODB_ENDPOINT not set; skipping the DynamoDB Local run (CI runs it)")
		}
		fn(t, dynamoSDK(t, ep), nil)
	})
}

func TestDynamo(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		storetest.Run(t, func(t *testing.T) store.Store {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			d := store.NewDynamo(db, "ripple_test_"+suffix(), store.WithLeaseSkew(0))
			if err := d.EnsureTable(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.DeleteTable(context.Background()) })
			return d
		})
	})
}

// The whole conformance suite again with several dependent shards, which the default
// (one) never exercises.
func TestDynamoSharded(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		storetest.Run(t, func(t *testing.T) store.Store {
			d := newDynamoTable(t, db, "ripple_test_"+suffix(), store.WithJobShards(5), store.WithDependentShards(3), store.WithLeaseSkew(0))
			return d
		})
	})
}

func newDynamoTable(t *testing.T, db *dynamodb.Client, table string, opts ...store.DynamoOption) *store.Dynamo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d := store.NewDynamo(db, table, opts...)
	if err := d.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTable(context.Background()) })
	return d
}

func pendingJob(i int, nb time.Time) model.Job {
	repo := fmt.Sprintf("acme/r%03d", i)
	return model.Job{Key: model.JobKey(repo, "go", "example.com/lib", "v1.0.0"), Repo: repo, Datasource: "go", Package: "example.com/lib",
		Version: "v1.0.0", ReleasedAt: nb, State: model.JobPending, NotBefore: nb}
}

// gsiCount counts the items under one GSI1PK value.
func gsiCount(t *testing.T, db *dynamodb.Client, table, pk string) int {
	t.Helper()
	out, err := db.Query(context.Background(), &dynamodb.QueryInput{
		TableName: &table, IndexName: aws.String("gsi1"), Select: types.SelectCount,
		KeyConditionExpression:    aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return int(out.Count)
}

// No index partition may hold most of the pending queue: this is the hot-partition guard
// against the real index, not just the key function.
func TestDynamoPendingQueueIsSpread(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table) // default shard count
		ctx := context.Background()
		const n = 400
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		for i := 0; i < n; i++ {
			if ok, err := d.EnqueueJob(ctx, pendingJob(i, now)); err != nil || !ok {
				t.Fatalf("enqueue: %v %v", ok, err)
			}
		}
		pks := []string{"JOBS#pending"}
		for i := 1; i < store.DefaultJobShards; i++ {
			pks = append(pks, fmt.Sprintf("JOBS#pending#%d", i))
		}
		total := 0
		for _, pk := range pks {
			c := gsiCount(t, db, table, pk)
			total += c
			if float64(c) > 1.5*float64(n)/store.DefaultJobShards || c == 0 {
				t.Errorf("partition %s holds %d of %d", pk, c, n)
			}
		}
		if total != n {
			t.Errorf("%d jobs in the shards, want %d", total, n)
		}
	})
}

// A table written before sharding (everything under the bare keys) keeps working.
func TestDynamoReadsUnshardedLayout(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table, store.WithJobShards(8), store.WithDependentShards(4))
		ctx := context.Background()
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		sv := func(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
		put := func(m map[string]types.AttributeValue) {
			if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &table, Item: m}); err != nil {
				t.Fatal(err)
			}
		}
		j := pendingJob(1, now.Add(-time.Minute))
		nbs := j.NotBefore.UTC().Format("2006-01-02T15:04:05.000000000Z")
		put(map[string]types.AttributeValue{"PK": sv("JOB#" + j.Key), "SK": sv("META"), "GSI1PK": sv("JOBS#pending"), "GSI1SK": sv(nbs + "\x1f" + j.Key),
			"Key": sv(j.Key), "Repo": sv(j.Repo), "Datasource": sv("go"), "Package": sv(j.Package), "Version": sv(j.Version),
			"ReleasedAt": sv(nbs), "NotBefore": sv(nbs), "State": sv(string(model.JobPending)), "Attempts": &types.AttributeValueMemberN{Value: "0"}})
		put(map[string]types.AttributeValue{"PK": sv("REPO#old/repo"), "SK": sv("DEP#go.mod\x1fgo\x1fexample.com/lib"), "GSI1PK": sv("PKG#go#example.com/lib"),
			"GSI1SK": sv("old/repo\x1fgo.mod"), "Repo": sv("old/repo"), "File": sv("go.mod"), "Manager": sv("gomod"), "Datasource": sv("go"), "Package": sv("example.com/lib"), "Version": sv("v1.0.0")})

		due, err := d.ListDueJobs(ctx, now, 10)
		if err != nil || len(due) != 1 || due[0].Key != j.Key {
			t.Fatalf("legacy job: %v %v", due, err)
		}
		deps, err := d.ListDependents(ctx, "go", "example.com/lib")
		if err != nil || len(deps) != 1 || deps[0].Repo != "old/repo" {
			t.Fatalf("legacy dependent: %v %v", deps, err)
		}
		// Writing them again moves them to the sharded layout without losing or duplicating them.
		if err := d.UpdateJob(ctx, due[0]); err != nil {
			t.Fatal(err)
		}
		if err := d.ReplaceDependencies(ctx, "old/repo", []model.Dependency{{Repo: "old/repo", File: "go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/lib", Version: "v1.0.0"}}); err != nil {
			t.Fatal(err)
		}
		if due, _ = d.ListDueJobs(ctx, now, 10); len(due) != 1 {
			t.Fatalf("after rewrite: %v", due)
		}
		if deps, _ = d.ListDependents(ctx, "go", "example.com/lib"); len(deps) != 1 {
			t.Fatalf("after rewrite: %v", deps)
		}
	})
}

// Lowering the setting must not strand items: the table remembers the highest count.
func TestDynamoShardCountOnlyGrows(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		table := "ripple_test_" + suffix()
		big := newDynamoTable(t, db, table, store.WithJobShards(8))
		ctx := context.Background()
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		const n = 60
		for i := 0; i < n; i++ {
			if _, err := big.EnqueueJob(ctx, pendingJob(i, now)); err != nil {
				t.Fatal(err)
			}
		}
		small := store.NewDynamo(db, table, store.WithJobShards(2))
		if err := small.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		due, err := small.ListDueJobs(ctx, now, n)
		if err != nil || len(due) != n {
			t.Fatalf("with a smaller setting %d of %d jobs are visible, err=%v", len(due), n, err)
		}
	})
}
