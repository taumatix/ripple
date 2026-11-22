package store_test

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
	"github.com/taumatix/ripple/internal/store/storetest"
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
		t.Skip("RIPPLE_TEST_POSTGRES_DSN not set; skipping Postgres store tests (CI runs them against a service container)")
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

// DynamoClient builds a client for DynamoDB Local from RIPPLE_TEST_DYNAMODB_ENDPOINT.
func dynamoClient(t *testing.T) *dynamodb.Client {
	ep := os.Getenv("RIPPLE_TEST_DYNAMODB_ENDPOINT")
	if ep == "" {
		t.Skip("RIPPLE_TEST_DYNAMODB_ENDPOINT not set; skipping DynamoDB store tests (CI runs them against DynamoDB Local)")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(ep) })
}

func TestDynamo(t *testing.T) {
	db := dynamoClient(t)
	storetest.Run(t, func(t *testing.T) store.Store {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d := store.NewDynamo(db, "ripple_test_"+suffix())
		if err := d.EnsureTable(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTable(context.Background()) })
		return d
	})
}
