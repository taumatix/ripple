package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func freshDep(repo, ver string) model.Dependency {
	return model.Dependency{Repo: repo, File: "go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/lib", Version: ver}
}

// A repo registered a moment ago is a dependent at once, although the index still lags
// (entry 120): ListDependents also reads the table for the registrations of the last minutes.
func TestDynamoListDependentsSeesRegistrationsTheIndexHasNotCaught(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, f *dynamofake.Fake) {
		ctx := context.Background()
		d := newDynamoTable(t, db, "ripple_test_"+suffix())
		count := func(want int, why string) []model.Dependency {
			t.Helper()
			got, err := d.ListDependents(ctx, "go", "example.com/lib")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != want {
				t.Fatalf("%s: %d dependents, want %d: %v", why, len(got), want, got)
			}
			return got
		}
		if err := d.ReplaceDependencies(ctx, "acme/old", []model.Dependency{freshDep("acme/old", "v1.0.0")}); err != nil {
			t.Fatal(err)
		}
		if f != nil {
			f.HoldIndex() // the index shows acme/old only
			defer f.ReleaseIndex()
		}
		if err := d.ReplaceDependencies(ctx, "acme/new", []model.Dependency{freshDep("acme/new", "v1.0.0")}); err != nil {
			t.Fatal(err)
		}
		count(2, "a registration the index has not caught")
		// A second scan of the same repo (version bump) is not a second dependent.
		if err := d.ReplaceDependencies(ctx, "acme/new", []model.Dependency{freshDep("acme/new", "v1.1.0")}); err != nil {
			t.Fatal(err)
		}
		got := count(2, "a rescan")
		for _, x := range got {
			if x.Repo == "acme/new" && x.Version != "v1.1.0" {
				t.Fatalf("version %s is stale", x.Version)
			}
		}
		// The index shows the first version of acme/new after a catch-up, then lags again.
		if f != nil {
			f.ReleaseIndex()
			f.HoldIndex()
			if err := d.ReplaceDependencies(ctx, "acme/new", []model.Dependency{freshDep("acme/new", "v1.2.0")}); err != nil {
				t.Fatal(err)
			}
			count(2, "a re-versioned registration")
		}
		// A repo registered and then dropping the dependency inside the window is not listed.
		if err := d.ReplaceDependencies(ctx, "acme/gone", []model.Dependency{freshDep("acme/gone", "v1.0.0")}); err != nil {
			t.Fatal(err)
		}
		if err := d.ReplaceDependencies(ctx, "acme/gone", nil); err != nil {
			t.Fatal(err)
		}
		count(2, "a dependency dropped inside the window")
		if f != nil {
			f.ReleaseIndex()
			count(2, "the index caught up")
		}
	})
}

// A registration older than the window is no longer read from the table (the index has
// had its say) and the read removes the leftover item.
func TestDynamoListDependentsIgnoresAndPrunesExpiredRegistrations(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		if err := d.ReplaceDependencies(ctx, "acme/new", []model.Dependency{freshDep("acme/new", "v1.0.0")}); err != nil {
			t.Fatal(err)
		}
		fresh := func() []map[string]types.AttributeValue {
			out, err := db.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
				KeyConditionExpression:    aws.String("PK = :pk"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":pk": sv("FRESH#PKG#go#example.com/lib")}})
			if err != nil {
				t.Fatal(err)
			}
			return out.Items
		}
		items := fresh()
		if len(items) != 1 {
			t.Fatalf("%d registration items, want 1", len(items))
		}
		items[0]["At"] = sv(time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000000000Z"))
		if _, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: items[0]}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.ListDependents(ctx, "go", "example.com/lib"); err != nil {
			t.Fatal(err)
		}
		if n := len(fresh()); n != 0 {
			t.Fatalf("%d expired registration items survive a read", n)
		}
	})
}
