package dynamofake_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/taumatix/ripple/internal/store/dynamofake"
)

func sv(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func nv(v string) types.AttributeValue { return &types.AttributeValueMemberN{Value: v} }

// client is the real SDK client pointed at a fake with a table shaped like the store's.
func client(t *testing.T) (*dynamodb.Client, *dynamofake.Fake) {
	t.Helper()
	f := dynamofake.New()
	t.Cleanup(f.Close)
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("a", "b", "")))
	if err != nil {
		t.Fatal(err)
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(f.URL()) })
	s := types.ScalarAttributeTypeS
	_, err = db.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String("t"), BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: s}, {AttributeName: aws.String("SK"), AttributeType: s},
			{AttributeName: aws.String("GSI1PK"), AttributeType: s}, {AttributeName: aws.String("GSI1SK"), AttributeType: s}},
		KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash}, {AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange}},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{{IndexName: aws.String("gsi1"),
			KeySchema:  []types.KeySchemaElement{{AttributeName: aws.String("GSI1PK"), KeyType: types.KeyTypeHash}, {AttributeName: aws.String("GSI1SK"), KeyType: types.KeyTypeRange}},
			Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, f
}

func put(t *testing.T, db *dynamodb.Client, it map[string]types.AttributeValue) {
	t.Helper()
	if _, err := db.PutItem(context.Background(), &dynamodb.PutItemInput{TableName: aws.String("t"), Item: it}); err != nil {
		t.Fatal(err)
	}
}

func apiCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return ""
}

func str(it map[string]types.AttributeValue, k string) string {
	return it[k].(*types.AttributeValueMemberS).Value
}

func TestQueryReturnsSortKeyOrderAndSparseIndex(t *testing.T) {
	db, _ := client(t)
	for _, k := range []string{"c", "a", "b"} {
		put(t, db, map[string]types.AttributeValue{"PK": sv("p" + k), "SK": sv("s"), "GSI1PK": sv("g"), "GSI1SK": sv(k)})
	}
	put(t, db, map[string]types.AttributeValue{"PK": sv("nogsi"), "SK": sv("s")})
	out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"), IndexName: aws.String("gsi1"),
		KeyConditionExpression: aws.String("GSI1PK = :g"), ExpressionAttributeValues: map[string]types.AttributeValue{":g": sv("g")}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range out.Items {
		got = append(got, str(it, "GSI1SK"))
	}
	if strings.Join(got, "") != "abc" {
		t.Fatalf("order %v", got)
	}
}

// DynamoDB applies Limit before FilterExpression and reports LastEvaluatedKey when it stopped on
// Limit, even when no item remains.
func TestQueryLimitCountsEvaluatedItemsNotMatches(t *testing.T) {
	db, _ := client(t)
	for i := 0; i < 4; i++ {
		keep := "n"
		if i == 3 {
			keep = "y"
		}
		put(t, db, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv(fmt.Sprint(i)), "Keep": sv(keep)})
	}
	q := func(limit int32, start map[string]types.AttributeValue) *dynamodb.QueryOutput {
		out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"), Limit: aws.Int32(limit), ExclusiveStartKey: start,
			KeyConditionExpression: aws.String("PK = :p"), FilterExpression: aws.String("Keep = :y"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p"), ":y": sv("y")}})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := q(3, nil)
	if len(first.Items) != 0 || first.ScannedCount != 3 || first.LastEvaluatedKey == nil {
		t.Fatalf("first page: %d items scanned %d lek %v", len(first.Items), first.ScannedCount, first.LastEvaluatedKey)
	}
	second := q(3, first.LastEvaluatedKey)
	if len(second.Items) != 1 || second.LastEvaluatedKey != nil {
		t.Fatalf("second page: %d items lek %v", len(second.Items), second.LastEvaluatedKey)
	}
	exact := q(1, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("2")})
	if len(exact.Items) != 1 || exact.LastEvaluatedKey == nil {
		t.Fatalf("Limit reached on the last item still returns a LastEvaluatedKey (as DynamoDB does): %+v", exact)
	}
}

func TestQueryStopsAtOneMegabyteAndPaginatesWithoutLossOrRepeats(t *testing.T) {
	db, _ := client(t)
	blob := strings.Repeat("x", 100<<10)
	const n = 25
	for i := 0; i < n; i++ {
		put(t, db, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv(fmt.Sprintf("%03d", i)), "Blob": sv(blob)})
	}
	seen, pages := map[string]bool{}, 0
	var start map[string]types.AttributeValue
	for {
		out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"), ExclusiveStartKey: start,
			KeyConditionExpression: aws.String("PK = :p"), ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p")}})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(out.Items) > 10 {
			t.Fatalf("a page of %d items of 100 KB exceeds 1 MB", len(out.Items))
		}
		for _, it := range out.Items {
			k := str(it, "SK")
			if seen[k] {
				t.Fatalf("%s returned twice", k)
			}
			seen[k] = true
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		start = out.LastEvaluatedKey
	}
	if len(seen) != n || pages < 3 {
		t.Fatalf("saw %d of %d items in %d pages", len(seen), n, pages)
	}
}

func TestIndexQueryPaginatesThroughEqualIndexKeys(t *testing.T) {
	db, _ := client(t)
	for i := 0; i < 5; i++ { // identical GSI keys: only the table key tells them apart
		put(t, db, map[string]types.AttributeValue{"PK": sv(fmt.Sprint("r", i)), "SK": sv("s"), "GSI1PK": sv("g"), "GSI1SK": sv("same")})
	}
	seen := map[string]bool{}
	var start map[string]types.AttributeValue
	for {
		out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"), IndexName: aws.String("gsi1"), Limit: aws.Int32(2), ExclusiveStartKey: start,
			KeyConditionExpression: aws.String("GSI1PK = :g"), ExpressionAttributeValues: map[string]types.AttributeValue{":g": sv("g")}})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range out.Items {
			k := str(it, "PK")
			if seen[k] {
				t.Fatalf("%s twice", k)
			}
			seen[k] = true
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		for _, a := range []string{"PK", "SK", "GSI1PK", "GSI1SK"} {
			if out.LastEvaluatedKey[a] == nil {
				t.Fatalf("index LastEvaluatedKey lacks %s", a)
			}
		}
		start = out.LastEvaluatedKey
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d", len(seen))
	}
}

func TestRejectionsMatchDynamoDB(t *testing.T) {
	db, _ := client(t)
	ctx := context.Background()
	tn := aws.String("t")
	cases := map[string]struct {
		code string
		err  func() error
	}{
		"consistent read on a GSI": {"ValidationException", func() error {
			_, err := db.Query(ctx, &dynamodb.QueryInput{TableName: tn, IndexName: aws.String("gsi1"), ConsistentRead: aws.Bool(true),
				KeyConditionExpression: aws.String("GSI1PK = :g"), ExpressionAttributeValues: map[string]types.AttributeValue{":g": sv("g")}})
			return err
		}},
		"unused expression value": {"ValidationException", func() error {
			_, err := db.Query(ctx, &dynamodb.QueryInput{TableName: tn, KeyConditionExpression: aws.String("PK = :p"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p"), ":unused": sv("x")}})
			return err
		}},
		"unsupported expression syntax": {"ValidationException", func() error {
			_, err := db.Query(ctx, &dynamodb.QueryInput{TableName: tn, KeyConditionExpression: aws.String("PK = :p"), FilterExpression: aws.String("contains(A, :p)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p")}})
			return err
		}},
		"missing table": {"ResourceNotFoundException", func() error {
			_, err := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String("nope"), Key: map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("b")}})
			return err
		}},
		"key attribute missing": {"ValidationException", func() error {
			_, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: tn, Item: map[string]types.AttributeValue{"PK": sv("a")}})
			return err
		}},
		"empty index key": {"ValidationException", func() error {
			_, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: tn, Item: map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("b"), "GSI1PK": sv("")}})
			return err
		}},
		"oversized item": {"ValidationException", func() error {
			_, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: tn, Item: map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("b"), "X": sv(strings.Repeat("x", 401<<10))}})
			return err
		}},
		"unimplemented operation": {"ValidationException", func() error {
			_, err := db.Scan(ctx, &dynamodb.ScanInput{TableName: tn})
			return err
		}},
	}
	for name, c := range cases {
		if got := apiCode(c.err()); got != c.code {
			t.Errorf("%s: error code %q, want %q", name, got, c.code)
		}
	}
}

func TestConditionalPutAndDelete(t *testing.T) {
	db, _ := client(t)
	ctx := context.Background()
	item := map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("b"), "N": nv("2")}
	in := &dynamodb.PutItemInput{TableName: aws.String("t"), Item: item, ConditionExpression: aws.String("attribute_not_exists(PK)")}
	if _, err := db.PutItem(ctx, in); err != nil {
		t.Fatal(err)
	}
	var cc *types.ConditionalCheckFailedException
	if _, err := db.PutItem(ctx, in); !errors.As(err, &cc) {
		t.Fatalf("second put: %v", err)
	}
	all := map[string]types.AttributeValue{":v": nv("2"), ":w": nv("10"), ":p": sv("a")}
	for _, c := range []struct {
		expr string
		ok   bool
	}{
		{"#n <= :v", true}, {"#n < :v", false}, {"#n > :v OR (#n = :v AND attribute_exists(PK))", true},
		{"NOT #n = :v", false}, {"#n BETWEEN :v AND :w", true}, {"begins_with(PK, :p)", true},
		{"missing <> :v", true}, {"missing = :v", false},
	} {
		vals := map[string]types.AttributeValue{}
		for k, v := range all {
			if strings.Contains(c.expr, k) {
				vals[k] = v
			}
		}
		var names map[string]string
		if strings.Contains(c.expr, "#n") {
			names = map[string]string{"#n": "N"}
		}
		in := &dynamodb.DeleteItemInput{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("b")},
			ConditionExpression: aws.String(c.expr), ExpressionAttributeNames: names}
		if len(vals) > 0 {
			in.ExpressionAttributeValues = vals
		}
		_, err := db.DeleteItem(ctx, in)
		failed := errors.As(err, &cc)
		if err != nil && !failed {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if failed == c.ok {
			t.Errorf("%q: condition passed=%v, want %v", c.expr, !failed, c.ok)
		}
		if c.ok { // the delete went through; put it back for the next case
			put(t, db, item)
		}
	}
}

func TestBatchWriteLimits(t *testing.T) {
	db, f := client(t)
	ctx := context.Background()
	mk := func(n int, dup bool) []types.WriteRequest {
		var r []types.WriteRequest
		for i := 0; i < n; i++ {
			k := fmt.Sprint(i)
			if dup {
				k = "same"
			}
			r = append(r, types.WriteRequest{PutRequest: &types.PutRequest{Item: map[string]types.AttributeValue{"PK": sv(k), "SK": sv("s")}}})
		}
		return r
	}
	call := func(r []types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error) {
		return db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{"t": r}})
	}
	if _, err := call(mk(26, false)); apiCode(err) != "ValidationException" {
		t.Errorf("26 requests: %v", err)
	}
	if _, err := call(mk(2, true)); apiCode(err) != "ValidationException" {
		t.Errorf("duplicate keys: %v", err)
	}
	if out, err := call(mk(25, false)); err != nil || len(out.UnprocessedItems) != 0 {
		t.Errorf("25 requests: %v %v", out, err)
	}
	f.DropOneFromNextBatch()
	out, err := call(mk(3, false))
	if err != nil || len(out.UnprocessedItems["t"]) != 1 {
		t.Errorf("throttled batch should leave one unprocessed: %v %v", out, err)
	}
}

func TestUpdateItemAddReturnsNewValue(t *testing.T) {
	db, _ := client(t)
	ctx := context.Background()
	for want := 1; want <= 2; want++ {
		out, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("c"), "SK": sv("m")},
			UpdateExpression: aws.String("ADD #c :one"), ExpressionAttributeNames: map[string]string{"#c": "Count"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":one": nv("1")}, ReturnValues: types.ReturnValueUpdatedNew})
		if err != nil {
			t.Fatal(err)
		}
		if got := out.Attributes["Count"].(*types.AttributeValueMemberN).Value; got != fmt.Sprint(want) {
			t.Fatalf("count %s, want %d", got, want)
		}
	}
}
