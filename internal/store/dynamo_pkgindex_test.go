package store_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// dropPackageIndexItems turns a table into one written before the package index existed.
func dropPackageIndexItems(t *testing.T, db *dynamodb.Client, table string, bases ...string) {
	t.Helper()
	for _, b := range bases {
		if _, err := db.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{TableName: aws.String(table),
			Key: map[string]types.AttributeValue{"PK": sv("PKGS"), "SK": sv(b)}}); err != nil {
			t.Fatal(err)
		}
	}
}

// packageIndex reads the package index partition, sorted.
func packageIndex(t *testing.T, db *dynamodb.Client, table string) []string {
	t.Helper()
	out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true),
		KeyConditionExpression: aws.String("PK = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": sv("PKGS")}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range out.Items {
		names = append(names, it["SK"].(*types.AttributeValueMemberS).Value)
	}
	return names // a Query returns sort-key order
}

// The hygiene commands find packages through the package index: a table from before the
// index is scanned once, and neither a second check nor a package added afterwards costs
// another Scan of the table.
func TestDynamoHygieneListsPackagesFromTheIndexNotAScan(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, fake *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 4)
		dropPackageIndexItems(t, db, table, hotBase)
		d := newDynamoTable(t, db, table)
		scans := func() int {
			if fake == nil {
				return 0
			}
			return fake.Calls("Scan")
		}
		before := scans()
		if drift, err := d.CheckDependentCounts(ctx); err != nil || len(drift) != 1 {
			t.Fatalf("first check = %+v, %v", drift, err)
		}
		if fake != nil && scans() == before {
			t.Fatal("a table from before the index was not scanned to build it")
		}
		built := scans()
		bases := packageIndex(t, db, table)
		if len(bases) != 1 || bases[0] != hotBase {
			t.Fatalf("packages = %v, want the hot package", bases)
		}
		if err := d.ReplaceDependencies(ctx, "acme/c1", []model.Dependency{coldDep("acme/c1")}); err != nil {
			t.Fatal(err)
		}
		bases = packageIndex(t, db, table)
		if len(bases) != 2 || bases[0] != "PKG#go#example.com/cold" || bases[1] != hotBase {
			t.Fatalf("packages after a write = %v, want cold and hot", bases)
		}
		if scans() != built {
			t.Fatalf("scans went from %d to %d: the packages were listed by scanning", built, scans())
		}
	})
}

// RecountAllDependents drops the index entry of a package nobody uses any more and does not
// count it as recounted.
func TestDynamoRecountAllPrunesAPackageWithoutDependents(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		seedLegacy(t, db, table, 2)
		d := newDynamoTable(t, db, table, store.WithAdaptiveDependents(5))
		if err := d.ReplaceDependencies(ctx, "acme/c1", []model.Dependency{coldDep("acme/c1")}); err != nil {
			t.Fatal(err)
		}
		if err := d.ReplaceDependencies(ctx, "acme/c1", nil); err != nil {
			t.Fatal(err)
		}
		n, err := d.RecountAllDependents(ctx)
		if err != nil || n != 1 {
			t.Fatalf("RecountAllDependents = %d, %v, want 1 (the cold package has no dependents)", n, err)
		}
		bases := packageIndex(t, db, table)
		if len(bases) != 1 || bases[0] != hotBase {
			t.Fatalf("packages after pruning = %v, want only the hot one", bases)
		}
	})
}
