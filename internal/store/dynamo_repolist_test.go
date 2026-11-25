package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func listAll(t *testing.T, rl store.RepoLister, page int) []string {
	t.Helper()
	var all []string
	after := ""
	for i := 0; i < 20; i++ {
		got, err := rl.ListRepos(context.Background(), after, page)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if len(got) < page {
			return all
		}
		after = got[len(got)-1]
	}
	t.Fatal("listing did not end")
	return nil
}

// Repos written before the listing existed (no index item) are found by the first listing,
// and a repo removed afterwards is no longer listed.
func TestDynamoListsReposThatPredateTheIndex(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, _ *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		for _, n := range []string{"acme/b", "acme/a", "acme/c"} {
			_, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: map[string]types.AttributeValue{
				"PK": sv("REPO#" + n), "SK": sv("META"), "full_name": sv(n), "default_branch": sv("main")}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := listAll(t, d, 2); !reflect.DeepEqual(got, []string{"acme/a", "acme/b", "acme/c"}) {
			t.Fatalf("legacy repos: %v", got)
		}
		if err := d.UpsertRepo(ctx, model.Repo{FullName: "acme/d", DefaultBranch: "main"}); err != nil {
			t.Fatal(err)
		}
		if err := d.DeleteRepo(ctx, "acme/b"); err != nil {
			t.Fatal(err)
		}
		if got := listAll(t, d, 2); !reflect.DeepEqual(got, []string{"acme/a", "acme/c", "acme/d"}) {
			t.Fatalf("after upsert and delete: %v", got)
		}
	})
}

// The index is built once: a second listing reads the index and does not Scan again, and a build
// that fails part-way leaves no marker, so the next listing builds again instead of serving a
// partial index. Scan counts need the in-process fake; the other backends check the listing only.
func TestDynamoRepoIndexIsBuiltOnceAndRetriedAfterAFailedBuild(t *testing.T) {
	eachDynamo(t, func(t *testing.T, db *dynamodb.Client, f *dynamofake.Fake) {
		ctx := context.Background()
		table := "ripple_test_" + suffix()
		d := newDynamoTable(t, db, table)
		for _, n := range []string{"acme/b", "acme/a"} {
			_, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: map[string]types.AttributeValue{
				"PK": sv("REPO#" + n), "SK": sv("META"), "full_name": sv(n), "default_branch": sv("main")}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if f != nil {
			f.ThrottleNext("Scan", 100)
			if _, err := d.ListRepos(ctx, "", 10); err == nil {
				t.Fatal("a listing whose build was throttled must fail, not serve a partial index")
			}
			f.ThrottleNext("Scan", 0)
		}
		if got := listAll(t, d, 10); !reflect.DeepEqual(got, []string{"acme/a", "acme/b"}) {
			t.Fatalf("after a failed build: %v", got)
		}
		scans := 0
		if f != nil {
			scans = f.Calls("Scan")
		}
		if got := listAll(t, d, 1); !reflect.DeepEqual(got, []string{"acme/a", "acme/b"}) {
			t.Fatalf("second listing: %v", got)
		}
		if f != nil && f.Calls("Scan") != scans {
			t.Fatalf("a built index was scanned again: %d -> %d Scan calls", scans, f.Calls("Scan"))
		}
	})
}
