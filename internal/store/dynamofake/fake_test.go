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
			_, err := db.ListTables(ctx, &dynamodb.ListTablesInput{})
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

func TestTransactWriteIsAllOrNothing(t *testing.T) {
	db, _ := client(t)
	ctx := context.Background()
	put(t, db, map[string]types.AttributeValue{"PK": sv("L"), "SK": sv("M"), "Owner": sv("a")})
	check := func(owner string) types.TransactWriteItem {
		return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
			TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("L"), "SK": sv("M")},
			ConditionExpression:       aws.String("#o = :o"),
			ExpressionAttributeNames:  map[string]string{"#o": "Owner"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":o": sv(owner)}}}
	}
	write := types.TransactWriteItem{Put: &types.Put{TableName: aws.String("t"),
		Item: map[string]types.AttributeValue{"PK": sv("J"), "SK": sv("M"), "V": nv("1")}}}

	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{check("b"), write}})
	var tc *types.TransactionCanceledException
	if !errors.As(err, &tc) {
		t.Fatalf("stale condition: err = %v, want TransactionCanceledException", err)
	}
	if len(tc.CancellationReasons) != 2 || aws.ToString(tc.CancellationReasons[0].Code) != "ConditionalCheckFailed" || aws.ToString(tc.CancellationReasons[1].Code) != "None" {
		t.Fatalf("reasons = %+v", tc.CancellationReasons)
	}
	got, _ := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("J"), "SK": sv("M")}})
	if len(got.Item) != 0 {
		t.Fatalf("cancelled transaction wrote %v", got.Item)
	}

	if _, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{check("a"), write}}); err != nil {
		t.Fatalf("holding condition: %v", err)
	}
	got, _ = db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("J"), "SK": sv("M")}})
	if len(got.Item) == 0 {
		t.Fatal("committed transaction wrote nothing")
	}

	del := types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"PK": sv("J"), "SK": sv("M")}}}
	if _, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{write, del}}); apiCode(err) != "ValidationException" {
		t.Fatalf("two actions on one item: %v", err)
	}
}

func TestBatchGetItemReadsKeysAndEnforcesLimits(t *testing.T) {
	db, f := client(t)
	var keys []map[string]types.AttributeValue
	for i := 0; i < 3; i++ {
		put(t, db, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv(fmt.Sprint(i)), "V": sv("v")})
		keys = append(keys, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv(fmt.Sprint(i))})
	}
	keys = append(keys, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("absent")})
	get := func(ks []map[string]types.AttributeValue) (*dynamodb.BatchGetItemOutput, error) {
		return db.BatchGetItem(context.Background(), &dynamodb.BatchGetItemInput{RequestItems: map[string]types.KeysAndAttributes{
			"t": {Keys: ks, ConsistentRead: aws.Bool(true)}}})
	}
	out, err := get(keys)
	if err != nil || len(out.Responses["t"]) != 3 || len(out.UnprocessedKeys) != 0 {
		t.Fatalf("got %d items, unprocessed %d, err %v; want the 3 present keys and no error for the absent one", len(out.Responses["t"]), len(out.UnprocessedKeys), err)
	}
	if _, err := get(append(keys, keys[0])); apiCode(err) != "ValidationException" {
		t.Fatalf("duplicate keys: %v, want ValidationException", err)
	}
	var many []map[string]types.AttributeValue
	for i := 0; i < 101; i++ {
		many = append(many, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv(fmt.Sprint(i))})
	}
	if _, err := get(many); apiCode(err) != "ValidationException" {
		t.Fatalf("101 keys: %v, want ValidationException", err)
	}
	f.DropOneFromNextBatchGet()
	out, err = get(keys[:3])
	if err != nil || len(out.Responses["t"]) != 2 || len(out.UnprocessedKeys["t"].Keys) != 1 {
		t.Fatalf("hook: %d items, %d unprocessed, err %v; want 2 and 1", len(out.Responses["t"]), len(out.UnprocessedKeys["t"].Keys), err)
	}
}

func TestScanFiltersAndPaginatesWithoutLossOrRepeats(t *testing.T) {
	db, _ := client(t)
	for i := 0; i < 10; i++ {
		kind := "a"
		if i%3 == 0 {
			kind = "b"
		}
		put(t, db, map[string]types.AttributeValue{"PK": sv(fmt.Sprintf("p%d", i%2)), "SK": sv(fmt.Sprint(i)), "Kind": sv(kind)})
	}
	var got []string
	var start map[string]types.AttributeValue
	pages := 0
	for {
		out, err := db.Scan(context.Background(), &dynamodb.ScanInput{TableName: aws.String("t"), Limit: aws.Int32(3), ExclusiveStartKey: start,
			FilterExpression: aws.String("Kind = :b"), ExpressionAttributeValues: map[string]types.AttributeValue{":b": sv("b")}})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, it := range out.Items {
			got = append(got, str(it, "SK"))
		}
		if start = out.LastEvaluatedKey; start == nil {
			break
		}
	}
	if len(got) != 4 || pages != 4 {
		t.Fatalf("scan found %v in %d pages, want the 4 'b' items (0,3,6,9) over 4 pages of 3 evaluated", got, pages)
	}
	out, err := db.Scan(context.Background(), &dynamodb.ScanInput{TableName: aws.String("t"), Select: types.SelectCount})
	if err != nil || out.Count != 10 || len(out.Items) != 0 {
		t.Fatalf("Select=COUNT: %+v %v", out, err)
	}
	if _, err := db.Scan(context.Background(), &dynamodb.ScanInput{TableName: aws.String("t"), IndexName: aws.String("gsi1")}); apiCode(err) != "ValidationException" {
		t.Fatalf("index scan is not implemented and must say so, got %v", err)
	}
}

// DynamoDB rejects a reserved word used bare in an expression; the store must alias it.
func TestReservedWordsMustBeAliased(t *testing.T) {
	db, _ := client(t)
	put(t, db, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("s"), "Name": sv("n")})
	q := func(filter string, names map[string]string) error {
		_, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"),
			KeyConditionExpression: aws.String("PK = :p"), FilterExpression: aws.String(filter), ExpressionAttributeNames: names,
			ExpressionAttributeValues: map[string]types.AttributeValue{":p": sv("p"), ":n": sv("n")}})
		return err
	}
	if err := q("Name = :n", nil); apiCode(err) != "ValidationException" || !strings.Contains(err.Error(), "reserved keyword") {
		t.Fatalf("bare reserved word: %v, want a ValidationException naming a reserved keyword", err)
	}
	if err := q("#n = :n", map[string]string{"#n": "Name"}); err != nil {
		t.Fatalf("aliased reserved word: %v", err)
	}
	if err := q("Nickname = :n", nil); err != nil {
		t.Fatalf("a word that is not reserved: %v", err)
	}
}

// A held index answers from the moment it was held, as a lagging GSI does; the table is not stale.
func TestHeldIndexServesStaleReads(t *testing.T) {
	db, f := client(t)
	idx := func() int {
		out, err := db.Query(context.Background(), &dynamodb.QueryInput{TableName: aws.String("t"), IndexName: aws.String("gsi1"), Select: types.SelectCount,
			KeyConditionExpression: aws.String("GSI1PK = :g"), ExpressionAttributeValues: map[string]types.AttributeValue{":g": sv("g")}})
		if err != nil {
			t.Fatal(err)
		}
		return int(out.Count)
	}
	put(t, db, map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("s"), "GSI1PK": sv("g"), "GSI1SK": sv("1")})
	f.HoldIndex()
	put(t, db, map[string]types.AttributeValue{"PK": sv("b"), "SK": sv("s"), "GSI1PK": sv("g"), "GSI1SK": sv("2")})
	if _, err := db.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{TableName: aws.String("t"),
		Key: map[string]types.AttributeValue{"PK": sv("a"), "SK": sv("s")}}); err != nil {
		t.Fatal(err)
	}
	if got := idx(); got != 1 {
		t.Fatalf("held index sees %d, want the 1 item present when it was held (a deleted, b not yet visible)", got)
	}
	if out, _ := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String("t"), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{"PK": sv("b"), "SK": sv("s")}}); out.Item == nil {
		t.Fatal("the table itself must not be stale")
	}
	f.ReleaseIndex()
	if got := idx(); got != 1 {
		t.Fatalf("released index sees %d, want 1 (only b remains)", got)
	}
	put(t, db, map[string]types.AttributeValue{"PK": sv("c"), "SK": sv("s"), "GSI1PK": sv("g"), "GSI1SK": sv("3")})
	if got := idx(); got != 2 {
		t.Fatalf("released index is live again: %d, want 2", got)
	}
}

// ThrottleNext answers with ProvisionedThroughputExceededException, which the SDK retries.
func TestThrottleNextIsRetriedByTheClientThenSurfaces(t *testing.T) {
	db, f := client(t)
	put(t, db, map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("s")})
	get := func() error {
		_, err := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String("t"),
			Key: map[string]types.AttributeValue{"PK": sv("p"), "SK": sv("s")}})
		return err
	}
	before := f.Calls("GetItem")
	f.ThrottleNext("GetItem", 1)
	if err := get(); err != nil {
		t.Fatalf("one throttle must be absorbed by the SDK's retries: %v", err)
	}
	if got := f.Calls("GetItem") - before; got != 2 {
		t.Fatalf("%d GetItem calls, want 2 (throttled, then served)", got)
	}
	f.ThrottleNext("GetItem", 100)
	if err := get(); apiCode(err) != "ProvisionedThroughputExceededException" {
		t.Fatalf("persistent throttling: %v, want ProvisionedThroughputExceededException", err)
	}
	f.ThrottleNext("GetItem", 0)
	if err := get(); err != nil {
		t.Fatalf("throttle cleared: %v", err)
	}
}
