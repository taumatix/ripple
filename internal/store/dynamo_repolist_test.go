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
