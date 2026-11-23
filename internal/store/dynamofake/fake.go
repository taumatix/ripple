// Package dynamofake is an in-process DynamoDB that speaks the real wire protocol (the JSON
// 1.0 protocol, X-Amz-Target dispatch) over httptest, so the store runs unchanged through the
// real AWS SDK client against it, with no container.
//
// It implements the operations the store uses: CreateTable, DescribeTable, DeleteTable,
// PutItem, GetItem, DeleteItem, UpdateItem (SET and ADD), Query (table and global secondary
// index) and BatchWriteItem. Semantics follow the API reference:
//
//   - Query returns items in sort-key order and evaluates at most Limit items and at most 1 MB
//     of item data per call, applying FilterExpression after that read; it returns
//     LastEvaluatedKey whenever it stopped on Limit or the size cap, even if nothing remains.
//   - ConsistentRead on a global secondary index is a ValidationException.
//   - BatchWriteItem takes at most 25 requests, rejects duplicate keys, and takes no condition.
//   - A failed condition is a ConditionalCheckFailedException; an unused expression attribute
//     name or value is a ValidationException.
//
// Where it differs from DynamoDB (see docs/store-dynamodb.md): the index is updated
// synchronously, so it is never eventually consistent; there is no throttling or partition
// limit; item and page sizes are an estimate; reserved words are not rejected; items with
// equal index keys come back ordered by table key (DynamoDB leaves that unspecified);
// TransactWriteItems, Scan, BatchGetItem and the rest of the expression language are not
// implemented and answer with a ValidationException naming the gap.
package dynamofake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// PageBytes is the data one Query reads before it stops and returns LastEvaluatedKey.
	PageBytes = 1 << 20
	// MaxItemBytes is the largest item DynamoDB stores.
	MaxItemBytes = 400 << 10
	// MaxBatch is the most requests one BatchWriteItem takes.
	MaxBatch = 25
)

// Fake is one in-memory DynamoDB endpoint.
type Fake struct {
	srv *httptest.Server

	mu      sync.Mutex
	tables  map[string]*table
	delay   time.Duration
	inQuery int
	maxQ    int
	queries int
	dropOne bool
}

type table struct {
	hash, rng string
	indexes   map[string][2]string // name -> hash, range attribute
	items     map[string]map[string]av
}

// New starts a fake endpoint, closed when the test ends if the caller defers Close.
func New() *Fake {
	f := &Fake{tables: map[string]*table{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// URL is the endpoint to give the SDK client as its BaseEndpoint.
func (f *Fake) URL() string { return f.srv.URL }

// Close stops the endpoint.
func (f *Fake) Close() { f.srv.Close() }

// SetQueryDelay makes every Query take at least d, so a test can observe concurrency.
func (f *Fake) SetQueryDelay(d time.Duration) { f.mu.Lock(); f.delay = d; f.mu.Unlock() }

// QueryStats reports how many Query calls were made and the most that ran at once.
func (f *Fake) QueryStats() (calls, maxConcurrent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries, f.maxQ
}

// ResetQueryStats zeroes QueryStats.
func (f *Fake) ResetQueryStats() { f.mu.Lock(); f.queries, f.maxQ = 0, 0; f.mu.Unlock() }

// DropOneFromNextBatch makes the next BatchWriteItem leave its last request unprocessed, as
// DynamoDB does under throttling, so the caller's retry path runs.
func (f *Fake) DropOneFromNextBatch() { f.mu.Lock(); f.dropOne = true; f.mu.Unlock() }

// Items returns a copy of every item in the table (for assertions on the stored layout).
func (f *Fake) Items(tableName string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tables[tableName]
	if t == nil {
		return nil
	}
	var out []map[string]any
	for _, it := range t.items {
		c := map[string]any{}
		for k, v := range it {
			c[k] = v
		}
		out = append(out, c)
	}
	return out
}

type apiError struct{ typ, msg string }

func (e *apiError) Error() string { return e.typ + ": " + e.msg }

func validation(msg string) error {
	return &apiError{"com.amazon.coral.validate#ValidationException", msg}
}
func notFound(msg string) error {
	return &apiError{"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException", msg}
}
func condFailed() error {
	return &apiError{"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException", "The conditional request failed"}
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
	var out any
	var err error
	switch op {
	case "CreateTable":
		out, err = f.createTable(body)
	case "DescribeTable":
		out, err = f.describeTable(body)
	case "DeleteTable":
		out, err = f.deleteTable(body)
	case "PutItem":
		out, err = f.putItem(body)
	case "GetItem":
		out, err = f.getItem(body)
	case "DeleteItem":
		out, err = f.deleteItem(body)
	case "UpdateItem":
		out, err = f.updateItem(body)
	case "Query":
		out, err = f.query(body)
	case "BatchWriteItem":
		out, err = f.batchWrite(body)
	default:
		err = validation("dynamofake does not implement " + op)
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	if err != nil {
		ae, ok := err.(*apiError)
		if !ok {
			ae = &apiError{"com.amazon.coral.service#InternalFailure", err.Error()}
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": ae.typ, "message": ae.msg})
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

func decode(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return validation("malformed request: " + err.Error())
	}
	return nil
}

type keySchema struct {
	AttributeName string
	KeyType       string
}

func schemaOf(ks []keySchema) (hash, rng string) {
	for _, k := range ks {
		if k.KeyType == "HASH" {
			hash = k.AttributeName
		} else {
			rng = k.AttributeName
		}
	}
	return
}

func (f *Fake) createTable(body []byte) (any, error) {
	var in struct {
		TableName              string
		KeySchema              []keySchema
		AttributeDefinitions   []struct{ AttributeName, AttributeType string }
		GlobalSecondaryIndexes []struct {
			IndexName string
			KeySchema []keySchema
		}
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	for _, a := range in.AttributeDefinitions {
		if a.AttributeType != "S" {
			return nil, validation("dynamofake supports string keys only")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tables[in.TableName] != nil {
		return nil, &apiError{"com.amazonaws.dynamodb.v20120810#ResourceInUseException", "Table already exists: " + in.TableName}
	}
	t := &table{indexes: map[string][2]string{}, items: map[string]map[string]av{}}
	t.hash, t.rng = schemaOf(in.KeySchema)
	if t.hash == "" || t.rng == "" {
		return nil, validation("dynamofake needs a hash and a range key")
	}
	for _, g := range in.GlobalSecondaryIndexes {
		h, r := schemaOf(g.KeySchema)
		t.indexes[g.IndexName] = [2]string{h, r}
	}
	f.tables[in.TableName] = t
	return map[string]any{"TableDescription": desc(in.TableName, t)}, nil
}

func desc(name string, t *table) map[string]any {
	return map[string]any{"TableName": name, "TableStatus": "ACTIVE", "ItemCount": len(t.items),
		"KeySchema": []keySchema{{t.hash, "HASH"}, {t.rng, "RANGE"}}}
}

func (f *Fake) describeTable(body []byte) (any, error) {
	var in struct{ TableName string }
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tables[in.TableName]
	if t == nil {
		return nil, notFound("Cannot do operations on a non-existent table")
	}
	return map[string]any{"Table": desc(in.TableName, t)}, nil
}

func (f *Fake) deleteTable(body []byte) (any, error) {
	var in struct{ TableName string }
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tables[in.TableName]
	if t == nil {
		return nil, notFound("Cannot do operations on a non-existent table")
	}
	delete(f.tables, in.TableName)
	return map[string]any{"TableDescription": desc(in.TableName, t)}, nil
}

// lookup returns the table; the caller holds f.mu.
func (f *Fake) lookup(name string) (*table, error) {
	if t := f.tables[name]; t != nil {
		return t, nil
	}
	return nil, notFound("Cannot do operations on a non-existent table")
}

func itemKey(t *table, it map[string]av) (string, error) {
	h, ok1 := str(it[t.hash])
	r, ok2 := str(it[t.rng])
	if !ok1 || !ok2 || h == "" || r == "" {
		return "", validation("One of the required keys was not given a value")
	}
	return h + "\x00" + r, nil
}

// checkItem enforces what DynamoDB checks on a written item: key attributes present, indexed
// attributes of the declared type and non-empty, total size at most 400 KB.
func (t *table) checkItem(it map[string]av) error {
	if _, err := itemKey(t, it); err != nil {
		return err
	}
	for _, ix := range t.indexes {
		for _, a := range ix {
			if v, ok := it[a]; ok {
				if s, isS := str(v); !isS || s == "" {
					return validation("One or more parameter values were invalid: Type mismatch or empty value for Index Key " + a)
				}
			}
		}
	}
	if sizeOfItem(it) > MaxItemBytes {
		return validation("Item size has exceeded the maximum allowed size")
	}
	return nil
}

func conditionOf(expr string, names map[string]string, values map[string]av) (*node, *scope, error) {
	sc := newScope(names, values)
	if expr == "" {
		return nil, sc, nil
	}
	n, err := parseCondition(expr, sc)
	return n, sc, err
}

func keyOf(t *table, k map[string]av) (string, error) {
	if len(k) != 2 {
		return "", validation("The provided key element does not match the schema")
	}
	return itemKey(t, k)
}

func (f *Fake) putItem(body []byte) (any, error) {
	var in struct {
		TableName                 string
		Item                      map[string]av
		ConditionExpression       string
		ExpressionAttributeNames  map[string]string
		ExpressionAttributeValues map[string]av
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	cond, sc, err := conditionOf(in.ConditionExpression, in.ExpressionAttributeNames, in.ExpressionAttributeValues)
	if err != nil {
		return nil, err
	}
	if err := sc.checkUnused(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.lookup(in.TableName)
	if err != nil {
		return nil, err
	}
	if err := t.checkItem(in.Item); err != nil {
		return nil, err
	}
	k, _ := itemKey(t, in.Item)
	if cond != nil && !cond.eval(t.items[k]) {
		return nil, condFailed()
	}
	t.items[k] = in.Item
	return map[string]any{}, nil
}

func (f *Fake) getItem(body []byte) (any, error) {
	var in struct {
		TableName string
		Key       map[string]av
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.lookup(in.TableName)
	if err != nil {
		return nil, err
	}
	k, err := keyOf(t, in.Key)
	if err != nil {
		return nil, err
	}
	if it, ok := t.items[k]; ok {
		return map[string]any{"Item": it}, nil
	}
	return map[string]any{}, nil
}

func (f *Fake) deleteItem(body []byte) (any, error) {
	var in struct {
		TableName                 string
		Key                       map[string]av
		ConditionExpression       string
		ExpressionAttributeNames  map[string]string
		ExpressionAttributeValues map[string]av
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	cond, sc, err := conditionOf(in.ConditionExpression, in.ExpressionAttributeNames, in.ExpressionAttributeValues)
	if err != nil {
		return nil, err
	}
	if err := sc.checkUnused(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.lookup(in.TableName)
	if err != nil {
		return nil, err
	}
	k, err := keyOf(t, in.Key)
	if err != nil {
		return nil, err
	}
	if cond != nil && !cond.eval(t.items[k]) {
		return nil, condFailed()
	}
	delete(t.items, k)
	return map[string]any{}, nil
}

func (f *Fake) updateItem(body []byte) (any, error) {
	var in struct {
		TableName                 string
		Key                       map[string]av
		UpdateExpression          string
		ConditionExpression       string
		ReturnValues              string
		ExpressionAttributeNames  map[string]string
		ExpressionAttributeValues map[string]av
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	sc := newScope(in.ExpressionAttributeNames, in.ExpressionAttributeValues)
	upd, err := parseUpdate(in.UpdateExpression, sc)
	if err != nil {
		return nil, err
	}
	var cond *node
	if in.ConditionExpression != "" {
		if cond, err = parseCondition(in.ConditionExpression, sc); err != nil {
			return nil, err
		}
	}
	if err := sc.checkUnused(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.lookup(in.TableName)
	if err != nil {
		return nil, err
	}
	k, err := keyOf(t, in.Key)
	if err != nil {
		return nil, err
	}
	old, exists := t.items[k]
	if cond != nil && !cond.eval(old) {
		return nil, condFailed()
	}
	next := map[string]av{}
	if exists {
		for a, v := range old {
			next[a] = v
		}
	} else {
		for a, v := range in.Key {
			next[a] = v
		}
	}
	for a, v := range upd.set {
		if a == t.hash || a == t.rng {
			return nil, validation("One or more parameter values were invalid: Cannot update attribute " + a + ". This attribute is part of the key")
		}
		next[a] = v
	}
	for a, v := range upd.add {
		if a == t.hash || a == t.rng {
			return nil, validation("Cannot update attribute " + a + ". This attribute is part of the key")
		}
		sum, err := addNumber(next[a], v)
		if err != nil {
			return nil, err
		}
		next[a] = sum
	}
	if err := t.checkItem(next); err != nil {
		return nil, err
	}
	t.items[k] = next
	out := map[string]any{}
	switch in.ReturnValues {
	case "ALL_NEW":
		out["Attributes"] = next
	case "UPDATED_NEW":
		changed := map[string]av{}
		for _, a := range upd.targets {
			changed[a] = next[a]
		}
		out["Attributes"] = changed
	case "", "NONE":
	default:
		return nil, validation("dynamofake supports ReturnValues NONE, ALL_NEW and UPDATED_NEW")
	}
	return out, nil
}

func addNumber(cur, delta av) (av, error) {
	d, ok := delta["N"].(string)
	if !ok {
		return nil, validation("dynamofake ADD supports numbers only")
	}
	base := "0"
	if cur != nil {
		if base, ok = cur["N"].(string); !ok {
			return nil, validation("An operand in the update expression has an incorrect data type")
		}
	}
	a, _ := parseNum(base)
	b, ok := parseNum(d)
	if !ok {
		return nil, validation("invalid number " + d)
	}
	return av{"N": formatNum(a + b)}, nil
}

func (f *Fake) batchWrite(body []byte) (any, error) {
	var in struct {
		RequestItems map[string][]struct {
			PutRequest    *struct{ Item map[string]av }
			DeleteRequest *struct{ Key map[string]av }
		}
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	n := 0
	for _, reqs := range in.RequestItems {
		n += len(reqs)
	}
	if n == 0 || n > MaxBatch {
		return nil, validation("Too many items requested for the BatchWriteItem call (limit 25, at least 1)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	drop := f.dropOne
	f.dropOne = false
	type op struct {
		t   *table
		key string
		put map[string]av
	}
	var ops []op
	seen := map[string]bool{}
	for name, reqs := range in.RequestItems {
		t, err := f.lookup(name)
		if err != nil {
			return nil, err
		}
		for _, r := range reqs {
			if (r.PutRequest == nil) == (r.DeleteRequest == nil) {
				return nil, validation("Supplied AttributeValue has more than one datatypes set, or none")
			}
			var o op
			o.t = t
			if r.PutRequest != nil {
				if err := t.checkItem(r.PutRequest.Item); err != nil {
					return nil, err
				}
				o.key, _ = itemKey(t, r.PutRequest.Item)
				o.put = r.PutRequest.Item
			} else {
				k, err := keyOf(t, r.DeleteRequest.Key)
				if err != nil {
					return nil, err
				}
				o.key = k
			}
			if seen[name+"\x00"+o.key] {
				return nil, validation("Provided list of item keys contains duplicates")
			}
			seen[name+"\x00"+o.key] = true
			ops = append(ops, o)
		}
	}
	unprocessed := map[string]any{}
	for i, o := range ops {
		if drop && i == len(ops)-1 {
			// Echo the request back unprocessed, as DynamoDB does under throttling.
			for name, reqs := range in.RequestItems {
				if t := f.tables[name]; t == o.t {
					unprocessed[name] = reqs[len(reqs)-1:]
				}
			}
			continue
		}
		if o.put != nil {
			o.t.items[o.key] = o.put
		} else {
			delete(o.t.items, o.key)
		}
	}
	return map[string]any{"UnprocessedItems": unprocessed}, nil
}

func (f *Fake) query(body []byte) (any, error) {
	var in struct {
		TableName                 string
		IndexName                 string
		KeyConditionExpression    string
		FilterExpression          string
		ProjectionExpression      string
		Select                    string
		ConsistentRead            bool
		ScanIndexForward          *bool
		Limit                     *int
		ExclusiveStartKey         map[string]av
		ExpressionAttributeNames  map[string]string
		ExpressionAttributeValues map[string]av
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.queries++
	f.inQuery++
	f.maxQ = max(f.maxQ, f.inQuery)
	delay := f.delay
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inQuery--; f.mu.Unlock() }()
	if delay > 0 {
		time.Sleep(delay)
	}

	sc := newScope(in.ExpressionAttributeNames, in.ExpressionAttributeValues)
	keyCond, err := parseCondition(in.KeyConditionExpression, sc)
	if err != nil || in.KeyConditionExpression == "" {
		return nil, orValidation(err, "Either the KeyConditions or KeyConditionExpression parameter must be specified in the request.")
	}
	var filter *node
	if in.FilterExpression != "" {
		if filter, err = parseCondition(in.FilterExpression, sc); err != nil {
			return nil, err
		}
	}
	var proj []string
	if in.ProjectionExpression != "" {
		if proj, err = parseProjection(in.ProjectionExpression, sc); err != nil {
			return nil, err
		}
	}
	if err := sc.checkUnused(); err != nil {
		return nil, err
	}
	if in.Limit != nil && *in.Limit < 1 {
		return nil, validation("1 validation error detected: Value '" + "0" + "' at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.lookup(in.TableName)
	if err != nil {
		return nil, err
	}
	hashA, rngA := t.hash, t.rng
	if in.IndexName != "" {
		ix, ok := t.indexes[in.IndexName]
		if !ok {
			return nil, validation("The table does not have the specified index: " + in.IndexName)
		}
		if in.ConsistentRead {
			return nil, validation("Consistent reads are not supported on global secondary indexes")
		}
		hashA, rngA = ix[0], ix[1]
	}
	pkVal, sortCond, err := splitKeyCondition(keyCond, hashA, rngA)
	if err != nil {
		return nil, err
	}

	type entry struct {
		it map[string]av
	}
	var cands []map[string]av
	for _, it := range t.items {
		if v, ok := it[hashA]; ok && equalAV(v, pkVal) {
			if _, ok := it[rngA]; !ok {
				continue // sparse: an item without both index keys is not in the index
			}
			if sortCond == nil || sortCond.eval(it) {
				cands = append(cands, it)
			}
		}
	}
	cmpItems := func(a, b map[string]av) int {
		if c, _ := compare(a[rngA], b[rngA]); c != 0 {
			return c
		}
		if c, _ := compare(a[t.hash], b[t.hash]); c != 0 {
			return c
		}
		c, _ := compare(a[t.rng], b[t.rng])
		return c
	}
	forward := in.ScanIndexForward == nil || *in.ScanIndexForward
	sort.Slice(cands, func(i, j int) bool {
		if forward {
			return cmpItems(cands[i], cands[j]) < 0
		}
		return cmpItems(cands[i], cands[j]) > 0
	})
	if in.ExclusiveStartKey != nil {
		start := map[string]av{}
		for k, v := range in.ExclusiveStartKey {
			start[k] = v
		}
		if _, ok := start[rngA]; !ok || start[t.hash] == nil || start[t.rng] == nil {
			return nil, validation("The provided starting key is invalid")
		}
		i := sort.Search(len(cands), func(i int) bool {
			if forward {
				return cmpItems(cands[i], start) > 0
			}
			return cmpItems(cands[i], start) < 0
		})
		cands = cands[i:]
	}

	var items []map[string]av
	scanned, bytes := 0, 0
	var last map[string]av
	var lek map[string]av
	for _, it := range cands {
		sz := sizeOfItem(it)
		if scanned > 0 && bytes+sz > PageBytes {
			lek = keyMap(t, in.IndexName != "", hashA, rngA, last)
			break
		}
		scanned++
		bytes += sz
		last = it
		if filter == nil || filter.eval(it) {
			items = append(items, it)
		}
		if in.Limit != nil && scanned >= *in.Limit {
			lek = keyMap(t, in.IndexName != "", hashA, rngA, last)
			break
		}
	}
	out := map[string]any{"Count": len(items), "ScannedCount": scanned}
	if lek != nil {
		out["LastEvaluatedKey"] = lek
	}
	if in.Select != "COUNT" {
		res := make([]map[string]av, 0, len(items))
		for _, it := range items {
			res = append(res, project(it, proj))
		}
		out["Items"] = res
	}
	return out, nil
}

func orValidation(err error, msg string) error {
	if err != nil {
		return err
	}
	return validation(msg)
}

func keyMap(t *table, index bool, hashA, rngA string, it map[string]av) map[string]av {
	k := map[string]av{t.hash: it[t.hash], t.rng: it[t.rng]}
	if index {
		k[hashA], k[rngA] = it[hashA], it[rngA]
	}
	return k
}

func project(it map[string]av, names []string) map[string]av {
	if names == nil {
		return it
	}
	out := map[string]av{}
	for _, n := range names {
		if v, ok := it[n]; ok {
			out[n] = v
		}
	}
	return out
}

// splitKeyCondition requires "hash = :v" optionally ANDed with one condition on the range
// key, which is what DynamoDB accepts, and returns the partition value and the range condition.
func splitKeyCondition(n *node, hashA, rngA string) (av, *node, error) {
	var terms []*node
	var flatten func(*node)
	flatten = func(x *node) {
		if x.kind == "and" {
			flatten(x.kids[0])
			flatten(x.kids[1])
			return
		}
		terms = append(terms, x)
	}
	flatten(n)
	var pk av
	var sortCond *node
	for _, x := range terms {
		attr := ""
		if len(x.args) > 0 {
			attr = x.args[0].attr
		}
		switch {
		case x.kind == "cmp" && x.op == "=" && attr == hashA && x.args[1].isVal && pk == nil:
			pk = x.args[1].val
		case attr == rngA && sortCond == nil && ((x.kind == "cmp" && x.op != "<>") || x.kind == "between" || (x.kind == "func" && x.op == "begins_with")):
			sortCond = x
		default:
			return nil, nil, validation("Query condition missed key schema element or is not supported: " + hashA)
		}
	}
	if pk == nil {
		return nil, nil, validation("Query condition missed key schema element: " + hashA)
	}
	return pk, sortCond, nil
}

func equalAV(a, b av) bool { c, ok := compare(a, b); return ok && c == 0 }
