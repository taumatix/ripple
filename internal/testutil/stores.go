// Package testutil holds the fakes the end-to-end tests drive: a GitHub API and git
// host, the Go module index, and every store backend.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func suffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ForEachStore runs fn against the in-memory store and, when the environment points
// at them, against real Postgres (RIPPLE_TEST_POSTGRES_DSN) and DynamoDB Local
// (RIPPLE_TEST_DYNAMODB_ENDPOINT). A missing backend skips with a message, unless
// RIPPLE_REQUIRE_SERVICES=1, which turns the skip into a failure; CI and `make test-services` set it.
func ForEachStore(t *testing.T, fn func(t *testing.T, s store.Store)) {
	t.Run("memory", func(t *testing.T) { fn(t, store.NewMemory()) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("RIPPLE_TEST_POSTGRES_DSN")
		if dsn == "" {
			SkipUnlessRequired(t, "RIPPLE_TEST_POSTGRES_DSN not set; skipping the Postgres run (`make test-services` starts one)")
		}
		s, err := store.NewPostgres(context.Background(), dsn, "t_"+suffix())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
	t.Run("dynamodb", func(t *testing.T) {
		ep := os.Getenv("RIPPLE_TEST_DYNAMODB_ENDPOINT")
		if ep == "" {
			SkipUnlessRequired(t, "RIPPLE_TEST_DYNAMODB_ENDPOINT not set; skipping the DynamoDB run (`make test-services` starts one)")
		}
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(ep) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d := store.NewDynamo(db, "ripple_e2e_"+suffix(), store.WithLeaseSkew(100*time.Millisecond))
		if err := d.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTable(context.Background()) })
		fn(t, d)
	})
}

// ForEachStoreWithFake is ForEachStore plus the in-process DynamoDB fake, which always
// runs. It is for tests worth the extra run locally, where there is no DynamoDB Local.
func ForEachStoreWithFake(t *testing.T, fn func(t *testing.T, s store.Store)) {
	t.Run("dynamodb-fake", func(t *testing.T) {
		f := dynamofake.New()
		t.Cleanup(f.Close)
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(f.URL()) })
		d := store.NewDynamo(db, "ripple_e2e_"+suffix(), store.WithLeaseSkew(100*time.Millisecond))
		if err := d.EnsureTable(context.Background()); err != nil {
			t.Fatal(err)
		}
		fn(t, d)
	})
	ForEachStore(t, fn)
}

// SkipUnlessRequired skips t with msg, or fails it when RIPPLE_REQUIRE_SERVICES=1:
// a run that asked for the services must not pass without having used them.
func SkipUnlessRequired(t testing.TB, msg string) {
	t.Helper()
	if os.Getenv("RIPPLE_REQUIRE_SERVICES") == "1" {
		t.Fatalf("RIPPLE_REQUIRE_SERVICES=1 but a service is missing: %s", msg)
	}
	t.Skip(msg)
}

// Handles is n store handles over one shared backend, as n replicas have: handle i stamps
// its wall-clock times (DynamoDB leases and heartbeats) skew[i] ahead of the real clock.
// Postgres judges time by the database, so its handles ignore the skew, which is the point
// of comparing them.
type Handles []store.Store

// ForEachSharedStore runs fn against the shared backends that can have several handles: the
// in-process DynamoDB fake (always), and Postgres and DynamoDB Local when the environment
// points at them (see ForEachStore for the skip rules). The memory store is left out: it is
// one process by definition. leaseSkew is the DynamoDB takeover margin the handles are built with.
func ForEachSharedStore(t *testing.T, skews []time.Duration, leaseSkew time.Duration, fn func(t *testing.T, h Handles)) {
	dyn := func(t *testing.T, endpoint string, cleanup bool) {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(endpoint) })
		table := "ripple_soak_" + suffix()
		var h Handles
		for _, sk := range skews {
			sk := sk
			h = append(h, store.NewDynamo(db, table, store.WithLeaseSkew(leaseSkew),
				store.WithClock(func() time.Time { return time.Now().Add(sk) })))
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := h[0].(*store.Dynamo).EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		if cleanup {
			t.Cleanup(func() { _ = h[0].(*store.Dynamo).DeleteTable(context.Background()) })
		}
		fn(t, h)
	}
	t.Run("dynamodb-fake", func(t *testing.T) {
		f := dynamofake.New()
		t.Cleanup(f.Close)
		dyn(t, f.URL(), false)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("RIPPLE_TEST_POSTGRES_DSN")
		if dsn == "" {
			SkipUnlessRequired(t, "RIPPLE_TEST_POSTGRES_DSN not set; skipping the Postgres run (`make test-services` starts one)")
		}
		schema := "t_" + suffix()
		var h Handles
		for range skews {
			s, err := store.NewPostgres(context.Background(), dsn, schema)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			h = append(h, s)
		}
		fn(t, h)
	})
	t.Run("dynamodb", func(t *testing.T) {
		ep := os.Getenv("RIPPLE_TEST_DYNAMODB_ENDPOINT")
		if ep == "" {
			SkipUnlessRequired(t, "RIPPLE_TEST_DYNAMODB_ENDPOINT not set; skipping the DynamoDB run (`make test-services` starts one)")
		}
		dyn(t, ep, true)
	})
}
