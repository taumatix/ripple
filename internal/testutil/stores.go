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
)

func suffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ForEachStore runs fn against the in-memory store and, when the environment points
// at them, against real Postgres (RIPPLE_TEST_POSTGRES_DSN) and DynamoDB Local
// (RIPPLE_TEST_DYNAMODB_ENDPOINT). A missing backend skips with a message; CI sets both.
func ForEachStore(t *testing.T, fn func(t *testing.T, s store.Store)) {
	t.Run("memory", func(t *testing.T) { fn(t, store.NewMemory()) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("RIPPLE_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("RIPPLE_TEST_POSTGRES_DSN not set; skipping the Postgres run (CI runs it against a service container)")
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
			t.Skip("RIPPLE_TEST_DYNAMODB_ENDPOINT not set; skipping the DynamoDB run (CI runs it against DynamoDB Local)")
		}
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(ep) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d := store.NewDynamo(db, "ripple_e2e_"+suffix())
		if err := d.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTable(context.Background()) })
		fn(t, d)
	})
}
