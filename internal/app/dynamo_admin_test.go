package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func TestDynamoAdminNeedsTheDynamoStore(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	for name, fn := range map[string]func() error{
		"check":    func() error { return DynamoCheckCounts(ctx, st, &bytes.Buffer{}) },
		"recount":  func() error { return DynamoRecount(ctx, st, &bytes.Buffer{}) },
		"backfill": func() error { return DynamoBackfillFailedJobs(ctx, st, &bytes.Buffer{}) },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "RIPPLE_STORE=dynamodb") {
			t.Errorf("%s on the memory store: %v, want a RIPPLE_STORE=dynamodb error", name, err)
		}
	}
}

func TestDynamoAdminCommandsReport(t *testing.T) {
	ctx := context.Background()
	f := dynamofake.New()
	t.Cleanup(f.Close)
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
	if err != nil {
		t.Fatal(err)
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(f.URL()) })
	legacy := store.NewDynamo(db, "admin")
	if err := legacy.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	dep := model.Dependency{Repo: "acme/a", File: "go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/x", Version: "v1"}
	if err := legacy.ReplaceDependencies(ctx, "acme/a", []model.Dependency{dep}); err != nil {
		t.Fatal(err)
	}
	d := store.NewDynamo(db, "admin", store.WithAdaptiveDependents(10))
	var out bytes.Buffer
	if err := DynamoCheckCounts(ctx, d, &out); err != nil || !strings.Contains(out.String(), "PKG#go#example.com/x") || !strings.Contains(out.String(), "indexed=1 counted=0") {
		t.Fatalf("check: %v, %q", err, out.String())
	}
	out.Reset()
	if err := DynamoRecount(ctx, d, &out); err != nil || !strings.Contains(out.String(), "recounted 1 package") {
		t.Fatalf("recount: %v, %q", err, out.String())
	}
	out.Reset()
	if err := DynamoCheckCounts(ctx, d, &out); err != nil || !strings.Contains(out.String(), "counters match") {
		t.Fatalf("check after: %v, %q", err, out.String())
	}
	out.Reset()
	if err := DynamoBackfillFailedJobs(ctx, d, &out); err != nil || !strings.Contains(out.String(), "wrote 0 ") {
		t.Fatalf("backfill: %v, %q", err, out.String())
	}
}
