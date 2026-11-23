package dynamofake_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/store/dynamofake"
)

// vendoredReservedWords reads the word list out of the vendored developer guide page.
func vendoredReservedWords(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../../docs/vendor/dynamodb/reserved-words.md")
	if err != nil {
		t.Fatal(err)
	}
	word := regexp.MustCompile(`^[A-Z_]+$`)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if word.MatchString(strings.TrimSpace(l)) {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// The fake rejects the whole documented list, no more and no fewer.
func TestReservedListMatchesVendoredDoc(t *testing.T) {
	doc := vendoredReservedWords(t)
	if len(doc) != 573 {
		t.Fatalf("vendored page lists %d words, the page fetched on 2026-11-23 had 573", len(doc))
	}
	got := dynamofake.ReservedWords()
	if len(got) != len(doc) {
		t.Fatalf("the fake holds %d reserved words, the documentation %d", len(got), len(doc))
	}
	in := map[string]bool{}
	for _, w := range got {
		in[w] = true
	}
	for _, w := range doc {
		if !in[w] {
			t.Errorf("%s is documented as reserved but the fake accepts it", w)
		}
	}
}

// Every reserved word is refused bare in each clause that takes an attribute name, and accepted aliased.
func TestEveryReservedWordMustBeAliasedInEveryClause(t *testing.T) {
	db, _ := client(t)
	ctx := context.Background()
	tn := aws.String("t")
	vals := map[string]types.AttributeValue{":p": sv("p"), ":v": sv("v")}
	clauses := map[string]func(w string, alias bool) error{
		"filter": func(w string, alias bool) error {
			_, err := db.Query(ctx, withAlias(&dynamodb.QueryInput{TableName: tn, KeyConditionExpression: aws.String("PK = :p"),
				FilterExpression: aws.String(attr(w, alias) + " = :v"), ExpressionAttributeValues: vals}, w, alias))
			return err
		},
		"key condition": func(w string, alias bool) error {
			if alias {
				return nil // an aliased word is not a key attribute, which is a different rejection
			}
			_, err := db.Query(ctx, withAlias(&dynamodb.QueryInput{TableName: tn,
				KeyConditionExpression: aws.String("PK = :p AND begins_with(" + attr(w, alias) + ", :v)"), ExpressionAttributeValues: vals}, w, alias))
			return err
		},
		"projection": func(w string, alias bool) error {
			_, err := db.Query(ctx, withAlias(&dynamodb.QueryInput{TableName: tn, KeyConditionExpression: aws.String("PK = :p"),
				ProjectionExpression: aws.String(attr(w, alias)), ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p")}}, w, alias))
			return err
		},
		"update": func(w string, alias bool) error {
			in := &dynamodb.UpdateItemInput{TableName: tn, Key: map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("s")},
				UpdateExpression: aws.String("SET " + attr(w, alias) + " = :v"), ExpressionAttributeValues: map[string]types.AttributeValue{":v": sv("v")}}
			if alias {
				in.ExpressionAttributeNames = map[string]string{"#a": w}
			}
			_, err := db.UpdateItem(ctx, in)
			return err
		},
		"nested path": func(w string, alias bool) error {
			if alias {
				return nil
			}
			_, err := db.Query(ctx, &dynamodb.QueryInput{TableName: tn, KeyConditionExpression: aws.String("PK = :p"),
				FilterExpression: aws.String("outer." + w + " = :v"), ExpressionAttributeValues: vals})
			return err
		},
	}
	for _, w := range vendoredReservedWords(t) {
		for name, run := range clauses {
			err := run(w, false)
			// The words that are also operators of the expression grammar fail as a syntax error, as they do on DynamoDB.
			grammar := map[string]bool{"AND": true, "OR": true, "NOT": true, "BETWEEN": true, "SET": true, "ADD": true}[w]
			if apiCode(err) != "ValidationException" || (!grammar && !strings.Contains(err.Error(), "reserved keyword")) {
				t.Fatalf("%s bare in %s: %v, want a ValidationException naming a reserved keyword", w, name, err)
			}
			if err := run(w, true); err != nil {
				t.Fatalf("%s aliased in %s: %v", w, name, err)
			}
		}
	}
}

func attr(w string, alias bool) string {
	if alias {
		return "#a"
	}
	return w
}

func withAlias(in *dynamodb.QueryInput, w string, alias bool) *dynamodb.QueryInput {
	if alias {
		in.ExpressionAttributeNames = map[string]string{"#a": w}
	}
	return in
}
