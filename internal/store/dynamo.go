package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// Dynamo is the Store backed by one DynamoDB table (single-table design).
//
// Access patterns (PK/SK = table keys, GSI1PK/GSI1SK = index "gsi1"):
//
//	repo        PK=REPO#<repo>        SK=META
//	dependency  PK=REPO#<repo>        SK=DEP#<file>\x1f<ds>\x1f<pkg>   GSI1PK=PKG#<ds>#<line>  GSI1SK=<repo>\x1f<file>
//	release     PK=PKG#<ds>#<pkg>     SK=REL#<version>                  (no GSI attributes)
//	job         PK=JOB#<key>          SK=META    GSI1PK=JOBS#pending[#<shard>] (only while pending)  GSI1SK=<not_before fixed-width>\x1f<key>
//	open job    PK=REPO#<repo>        SK=OPENJOB#<key>  a copy of the job, present only while it is pending or failed (no GSI attributes)
//	shard marks PK=CONFIG#shards      SK=META    (high-water mark of both shard counts, and whether adaptive sharding was ever on)
//	pkg shards  PK=PKGCOUNT#<ds>#<line> SK=META  Deps (approximate dependent count), Shards (high-water, adaptive sharding only)
//	cursor      PK=CURSOR#<name>      SK=META
//
// Reverse lookup (package -> repos) is a Query on gsi1 with GSI1PK=PKG#<ds>#<pkg>.
// The index is sparse: only dependency items carry that key shape. Replacing a
// repo's dependencies is a Query on the base table by PK + SK prefix DEP#, then a
// diffed BatchWrite. Due jobs are a Query on GSI1PK=JOBS#pending with
// GSI1SK <= now. See docs/store-dynamodb.md for the trade-offs.
type Dynamo struct {
	db    *dynamodb.Client
	table string
	// jobShards and depShards are how many GSI partitions the pending queue and each
	// package's dependents are spread over. EnsureTable raises them to the high-water
	// mark recorded in the table, so a restart with smaller settings still reads every
	// shard that holds items. See dynamo_shard.go.
	jobShards, depShards int
	// concurrency bounds how many shard Queries one read runs at once (1 = sequential).
	concurrency int
	// adaptive is the dependents-per-shard threshold (0 = adaptive sharding off).
	// adaptiveSeen is set once the table has ever been written with it on, after which
	// reads and writes consult the per-package shard record even if the option is off.
	adaptive     int
	stripes      int
	adaptiveSeen bool
	// pkgShards caches each package's recorded shard count for the write path. It only
	// grows, and a stale (lower) value is safe to write with; reads never use it.
	pkgShards sync.Map
	// leaseSkew is the clock-skew margin of lease takeover (see dynamo_lease.go).
	leaseSkew time.Duration
	// marksBuilt is set once every live event is known to have its marker (dynamo_eventmarks.go).
	marksBuilt atomic.Bool
}

// DynamoOption configures NewDynamo.
type DynamoOption func(*Dynamo)

// WithJobShards sets how many partitions hold pending jobs (1..MaxShards, default DefaultJobShards).
func WithJobShards(n int) DynamoOption { return func(d *Dynamo) { d.jobShards = clampShards(n) } }

// WithDependentShards sets how many partitions hold one package's dependents
// (1..MaxShards, default DefaultDependentShards). Every ListDependents costs this many Queries.
func WithDependentShards(n int) DynamoOption { return func(d *Dynamo) { d.depShards = clampShards(n) } }

// WithQueryConcurrency bounds how many shard Queries one read runs at once
// (1..MaxShards, default DefaultQueryConcurrency; 1 reads shards one after another).
// The merged result does not depend on it.
func WithQueryConcurrency(n int) DynamoOption {
	return func(d *Dynamo) { d.concurrency = clampShards(n) }
}

// WithAdaptiveDependents turns on per-package dependent sharding: a package's dependents
// move to more GSI partitions as their number grows, so the default stays one partition
// per package and only a hot package pays for fan-out. perShard is how many dependents one
// partition may hold before the package's shard count doubles (0 leaves it off). See
// dynamo_shard.go for the rule.
func WithAdaptiveDependents(perShard int) DynamoOption {
	return func(d *Dynamo) { d.adaptive = max(perShard, 0) }
}

// WithCounterStripes spreads the writes of each package's dependents counter over n items
// (1..MaxShards, default 1: one item). A package imported by very many repos otherwise
// funnels every counter update through one item, whose partition limit is a write ceiling.
// The count is the sum over the stripes and the counter item written before striping, so the
// option can be turned on or off on a live table. It only matters with WithAdaptiveDependents.
func WithCounterStripes(n int) DynamoOption { return func(d *Dynamo) { d.stripes = clampShards(n) } }

const (
	gsi1 = "gsi1"
	sep  = "\x1f"
	tsFm = "2006-01-02T15:04:05.000000000Z"
)

func NewDynamo(db *dynamodb.Client, table string, opts ...DynamoOption) *Dynamo {
	d := &Dynamo{db: db, table: table, jobShards: DefaultJobShards, depShards: DefaultDependentShards, concurrency: DefaultQueryConcurrency, leaseSkew: DefaultLeaseSkew, stripes: 1}
	for _, o := range opts {
		o(d)
	}
	return d
}

// EnsureTable creates the table and its index when absent, records the shard counts, and
// backfills the open-job copies of pending jobs written before they existed.
func (d *Dynamo) EnsureTable(ctx context.Context) error {
	if err := d.createTable(ctx); err != nil {
		return err
	}
	if err := d.reconcileShards(ctx); err != nil {
		return err
	}
	return d.backfillOpenJobs(ctx)
}

// reconcileShards keeps a high-water mark of each shard count in the table
// (PK=CONFIG#shards) and adopts it when it exceeds the configured count: lowering the
// setting must not strand items in shards that are no longer read. Raising it records
// the new mark first, then reads and writes use it.
func (d *Dynamo) reconcileShards(ctx context.Context) error {
	var a struct {
		item
		Jobs, Dependents int
		Adaptive         int
	}
	ok, err := d.get(ctx, "CONFIG#shards", "META", &a)
	if err != nil {
		return wrapD("read shard config", err)
	}
	if ok {
		d.jobShards, d.depShards = max(d.jobShards, clampShards(a.Jobs)), max(d.depShards, clampShards(a.Dependents))
	}
	d.adaptiveSeen = d.adaptiveSeen || d.adaptive > 0 || (ok && a.Adaptive > 0)
	adaptive := 0
	if d.adaptiveSeen {
		adaptive = 1
	}
	if ok && a.Jobs >= d.jobShards && a.Dependents >= d.depShards && a.Adaptive >= adaptive {
		return nil
	}
	m, err := attributevalue.MarshalMap(struct {
		item
		Jobs, Dependents int
		Adaptive         int
	}{item{PK: "CONFIG#shards", SK: "META"}, d.jobShards, d.depShards, adaptive})
	if err != nil {
		return err
	}
	_, err = d.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &d.table, Item: m,
		ConditionExpression:      aws.String("attribute_not_exists(PK) OR (#j <= :j AND #d <= :d AND (attribute_not_exists(#a) OR #a <= :a))"),
		ExpressionAttributeNames: map[string]string{"#j": "Jobs", "#d": "Dependents", "#a": "Adaptive"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":a": &types.AttributeValueMemberN{Value: strconv.Itoa(adaptive)},
			":j": &types.AttributeValueMemberN{Value: strconv.Itoa(d.jobShards)}, ":d": &types.AttributeValueMemberN{Value: strconv.Itoa(d.depShards)}},
	})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		// Another instance recorded a mark above ours in one dimension: adopt it.
		return d.reconcileShards(ctx)
	}
	return wrapD("write shard config", err)
}

func (d *Dynamo) createTable(ctx context.Context) error {
	_, err := d.db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &d.table})
	if err == nil {
		return nil
	}
	var nf *types.ResourceNotFoundException
	if !errors.As(err, &nf) {
		return fmt.Errorf("dynamodb describe table: %w", err)
	}
	s := types.ScalarAttributeTypeS
	_, err = d.db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   &d.table,
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: s}, {AttributeName: aws.String("SK"), AttributeType: s},
			{AttributeName: aws.String("GSI1PK"), AttributeType: s}, {AttributeName: aws.String("GSI1SK"), AttributeType: s},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash}, {AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{{
			IndexName: aws.String(gsi1),
			KeySchema: []types.KeySchemaElement{
				{AttributeName: aws.String("GSI1PK"), KeyType: types.KeyTypeHash}, {AttributeName: aws.String("GSI1SK"), KeyType: types.KeyTypeRange},
			},
			Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
		}},
	})
	if err != nil {
		return fmt.Errorf("dynamodb create table: %w", err)
	}
	w := dynamodb.NewTableExistsWaiter(d.db)
	if err := w.Wait(ctx, &dynamodb.DescribeTableInput{TableName: &d.table}, 2*time.Minute); err != nil {
		return fmt.Errorf("dynamodb wait for table: %w", err)
	}
	return nil
}

// DeleteTable drops the table (used by tests).
func (d *Dynamo) DeleteTable(ctx context.Context) error {
	_, err := d.db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: &d.table})
	return err
}

func (d *Dynamo) Close() error { return nil }

type item struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	GSI1PK string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK string `dynamodbav:"GSI1SK,omitempty"`
}

// Flat copies of model types keep attribute names stable independent of model changes.
type repoAttrs struct {
	item
	FullName       string `dynamodbav:"full_name"`
	InstallationID int64  `dynamodbav:"installation_id"`
	DefaultBranch  string `dynamodbav:"default_branch"`
	Config         []byte `dynamodbav:"config,omitempty"`
	RepoID         int64  `dynamodbav:"repo_id,omitempty"`
}

type depAttrs struct {
	item
	Repo, File, Manager, Datasource, Package, Version string
}

type jobAttrs struct {
	item
	Key, Repo, Datasource, Package, Version string
	ReleasedAt, NotBefore                   string
	State                                   string
	Attempts                                int
	PRURL, Reason                           string
}

func ts(t time.Time) string { return t.UTC().Format(tsFm) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(tsFm, s)
	return t
}

func pkgBase(ds, pkg string) string { return "PKG#" + ds + "#" + model.Line(ds, pkg) }

func (st *Dynamo) depKeys(d model.Dependency, shards int) item {
	return item{
		PK: "REPO#" + d.Repo, SK: "DEP#" + d.File + sep + d.Datasource + sep + d.Package,
		GSI1PK: dependentsPK(pkgBase(d.Datasource, d.Package), shardOf(d.Repo, shards)), GSI1SK: d.Repo + sep + d.File,
	}
}

// depVersionSep starts the version suffix of a dependency sort key. Only the second and later
// versions of one (file, datasource, package) carry it.
const depVersionSep = "\x1e"

func n(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}

func (d *Dynamo) putCond(ctx context.Context, v any, cond string, vals map[string]types.AttributeValue) error {
	m, err := attributevalue.MarshalMap(v)
	if err != nil {
		return err
	}
	in := &dynamodb.PutItemInput{TableName: &d.table, Item: m, ConditionExpression: &cond, ExpressionAttributeValues: vals}
	if strings.Contains(cond, "#o") {
		in.ExpressionAttributeNames = map[string]string{"#o": "Owner", "#t": "Token", "#e": "Expires"}
	}
	_, err = d.db.PutItem(ctx, in)
	return err
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func (d *Dynamo) put(ctx context.Context, v any, cond *string) error {
	m, err := attributevalue.MarshalMap(v)
	if err != nil {
		return err
	}
	in := &dynamodb.PutItemInput{TableName: &d.table, Item: m, ConditionExpression: cond}
	_, err = d.db.PutItem(ctx, in)
	return err
}

func (d *Dynamo) get(ctx context.Context, pk, sk string, out any) (bool, error) {
	r, err := d.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table, ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{"PK": s(pk), "SK": s(sk)},
	})
	if err != nil {
		return false, err
	}
	if r.Item == nil {
		return false, nil
	}
	return true, attributevalue.UnmarshalMap(r.Item, out)
}

func (d *Dynamo) query(ctx context.Context, in *dynamodb.QueryInput, limit int, each func(map[string]types.AttributeValue) error) error {
	in.TableName = &d.table
	n := 0
	for {
		out, err := d.db.Query(ctx, in)
		if err != nil {
			return err
		}
		for _, it := range out.Items {
			if err := each(it); err != nil {
				return err
			}
			n++
			if limit > 0 && n >= limit {
				return nil
			}
		}
		if out.LastEvaluatedKey == nil {
			return nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

func (d *Dynamo) UpsertRepo(ctx context.Context, r model.Repo) error {
	err := d.put(ctx, repoAttrs{item: item{PK: "REPO#" + r.FullName, SK: "META"}, FullName: r.FullName,
		InstallationID: r.InstallationID, DefaultBranch: r.DefaultBranch, Config: r.Config, RepoID: r.RepoID}, nil)
	if err == nil {
		err = d.putRepoIndex(ctx, r.FullName) // the listing (dynamo_repolist.go)
	}
	return wrapD("upsert repo", err)
}

func (d *Dynamo) GetRepo(ctx context.Context, n string) (model.Repo, bool, error) {
	var a repoAttrs
	ok, err := d.get(ctx, "REPO#"+n, "META", &a)
	if err != nil || !ok {
		return model.Repo{}, false, wrapD("get repo", err)
	}
	return model.Repo{FullName: a.FullName, InstallationID: a.InstallationID, DefaultBranch: a.DefaultBranch, Config: a.Config, RepoID: a.RepoID}, true, nil
}

func (d *Dynamo) DeleteRepo(ctx context.Context, n string) error {
	var keys []map[string]types.AttributeValue
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("REPO#" + n)},
		ProjectionExpression:      aws.String("PK, SK"),
	}, 0, func(it map[string]types.AttributeValue) error { keys = append(keys, it); return nil })
	if err != nil {
		return wrapD("delete repo", err)
	}
	var reqs []types.WriteRequest
	for _, k := range keys {
		reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: k}})
	}
	if err := d.batch(ctx, reqs); err != nil {
		return wrapD("delete repo", err)
	}
	delta := map[string]int{}
	for _, k := range keys {
		if sk := k["SK"].(*types.AttributeValueMemberS).Value; strings.HasPrefix(sk, "DEP#") {
			delta[depBaseOf(sk)]--
		}
	}
	if err := d.deleteRepoIndex(ctx, n); err != nil {
		return wrapD("delete repo", err)
	}
	return wrapD("delete repo", d.adjustCounts(ctx, delta))
}

func (d *Dynamo) batch(ctx context.Context, reqs []types.WriteRequest) error {
	for len(reqs) > 0 {
		n := min(25, len(reqs))
		chunk := reqs[:n]
		reqs = reqs[n:]
		for attempt := 0; len(chunk) > 0; attempt++ {
			out, err := d.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{d.table: chunk}})
			if err != nil {
				return err
			}
			chunk = out.UnprocessedItems[d.table]
			if len(chunk) > 0 {
				if attempt > 8 {
					return errors.New("batch write: unprocessed items remain after retries")
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
				}
			}
		}
	}
	return nil
}

func (d *Dynamo) existingDepKeys(ctx context.Context, repo string) (map[string]map[string]types.AttributeValue, error) {
	have := map[string]map[string]types.AttributeValue{}
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk AND begins_with(SK, :p)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("REPO#" + repo), ":p": s("DEP#")},
		ProjectionExpression:      aws.String("PK, SK"),
	}, 0, func(it map[string]types.AttributeValue) error {
		have[it["SK"].(*types.AttributeValueMemberS).Value] = it
		return nil
	})
	return have, err
}

// ReplaceDependencies is not atomic across items: callers serialise per repo.
func (d *Dynamo) ReplaceDependencies(ctx context.Context, repo string, deps []model.Dependency) error {
	have, err := d.existingDepKeys(ctx, repo)
	if err != nil {
		return wrapD("replace dependencies", err)
	}
	var reqs []types.WriteRequest
	seen := map[string]bool{}
	delta := map[string]int{}
	shards := map[string]int{}
	deps = append([]model.Dependency(nil), deps...)
	SortDependencies(deps)
	rows := map[string]bool{}
	for _, dep := range deps {
		dep.Repo = repo
		base := pkgBase(dep.Datasource, dep.Package)
		n, ok := shards[base]
		if !ok {
			if n, err = d.depShardsFor(ctx, base, false); err != nil {
				return wrapD("replace dependencies", err)
			}
			shards[base] = n
		}
		k := d.depKeys(dep, n)
		if rows[k.SK+"\x1e"+dep.Version] {
			continue // the same row listed twice
		}
		rows[k.SK+"\x1e"+dep.Version] = true
		if seen[k.SK] {
			// A second version of one package in one file: the first row (lowest version)
			// keeps the plain key, so rows written before this existed stay valid.
			k.SK += depVersionSep + dep.Version
		}
		seen[k.SK] = true
		if _, had := have[k.SK]; !had {
			delta[base]++
		}
		m, err := attributevalue.MarshalMap(depAttrs{item: k, Repo: repo, File: dep.File, Manager: dep.Manager, Datasource: dep.Datasource, Package: dep.Package, Version: dep.Version})
		if err != nil {
			return err
		}
		reqs = append(reqs, types.WriteRequest{PutRequest: &types.PutRequest{Item: m}})
	}
	for sk, key := range have {
		if !seen[sk] {
			delta[depBaseOf(sk)]--
			reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: key}})
		}
	}
	if err := d.batch(ctx, reqs); err != nil {
		return wrapD("replace dependencies", err)
	}
	return wrapD("replace dependencies", d.adjustCounts(ctx, delta))
}

func (d *Dynamo) collectDeps(ctx context.Context, in *dynamodb.QueryInput) ([]model.Dependency, error) {
	var out []model.Dependency
	err := d.query(ctx, in, 0, func(it map[string]types.AttributeValue) error {
		var a depAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		out = append(out, model.Dependency{Repo: a.Repo, File: a.File, Manager: a.Manager, Datasource: a.Datasource, Package: a.Package, Version: a.Version})
		return nil
	})
	SortDependencies(out)
	return out, err
}

func (d *Dynamo) ListRepoDependencies(ctx context.Context, repo string) ([]model.Dependency, error) {
	out, err := d.collectDeps(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk AND begins_with(SK, :p)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("REPO#" + repo), ":p": s("DEP#")},
	})
	return out, wrapD("list repo dependencies", err)
}

// ListDependents reads gsi1, which is eventually consistent. It issues one Query per
// dependent shard of the package (at most MaxShards, default one), concurrently up to the
// configured bound; the merged result is sorted, so it does not depend on completion order.
func (d *Dynamo) ListDependents(ctx context.Context, ds, pkg string) ([]model.Dependency, error) {
	base := pkgBase(ds, pkg)
	n, err := d.depShardsFor(ctx, base, true)
	if err != nil {
		return nil, wrapD("list dependents", err)
	}
	parts, err := fanOut(ctx, n, d.concurrency, func(ctx context.Context, sh int) ([]model.Dependency, error) {
		return d.collectDeps(ctx, &dynamodb.QueryInput{
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(dependentsPK(base, sh))},
		})
	})
	if err != nil {
		return nil, wrapD("list dependents", err)
	}
	var out []model.Dependency
	for _, p := range parts {
		out = append(out, p...)
	}
	SortDependencies(out)
	return out, nil
}

func (d *Dynamo) RecordRelease(ctx context.Context, r model.Release) (bool, error) {
	err := d.put(ctx, struct {
		item
		Time string
	}{item{PK: "PKG#" + r.Datasource + "#" + r.Package, SK: "REL#" + r.Version}, ts(r.Time)}, aws.String("attribute_not_exists(PK)"))
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return false, nil
	}
	return err == nil, wrapD("record release", err)
}

// ListReleases is a consistent Query on the release partition of one package.
func (d *Dynamo) ListReleases(ctx context.Context, ds, pkg string) ([]model.Release, error) {
	var out []model.Release
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk AND begins_with(SK, :p)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("PKG#" + ds + "#" + pkg), ":p": s("REL#")},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a struct {
			SK   string
			Time string
		}
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		out = append(out, model.Release{Datasource: ds, Package: pkg, Version: strings.TrimPrefix(a.SK, "REL#"), Time: parseTS(a.Time)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, wrapD("list releases", err)
}

func (d *Dynamo) jobItem(j model.Job) jobAttrs {
	a := jobAttrs{item: item{PK: "JOB#" + j.Key, SK: "META"}, Key: j.Key, Repo: j.Repo, Datasource: j.Datasource, Package: j.Package,
		Version: j.Version, ReleasedAt: ts(j.ReleasedAt), NotBefore: ts(j.NotBefore), State: string(j.State), Attempts: j.Attempts, PRURL: j.PRURL, Reason: j.Reason}
	if j.State == model.JobPending {
		a.GSI1PK, a.GSI1SK = pendingJobsPK(shardOf(j.Key, d.jobShards)), ts(j.NotBefore)+sep+j.Key
	}
	return a
}

func (d *Dynamo) EnqueueJob(ctx context.Context, j model.Job) (bool, error) {
	err := d.put(ctx, d.jobItem(j), aws.String("attribute_not_exists(PK)"))
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return false, nil
	}
	if err == nil {
		err = d.syncOpenJob(ctx, j)
	}
	return err == nil, wrapD("enqueue job", err)
}

func isOpen(j model.Job) bool { return j.State == model.JobPending || j.State == model.JobFailed }

// syncOpenJob keeps the repo-partition copy of a job in step with it: written while
// the job is pending or failed, removed once it is not. It is a second write after
// the job's own, so a failure between the two leaves the copy stale until the job's
// next update; only the dependency dashboard reads it.
func (d *Dynamo) syncOpenJob(ctx context.Context, j model.Job) error {
	if isOpen(j) {
		a := d.jobItem(j)
		a.item = item{PK: "REPO#" + j.Repo, SK: "OPENJOB#" + j.Key}
		return d.put(ctx, a, nil)
	}
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &d.table,
		Key: map[string]types.AttributeValue{"PK": s("REPO#" + j.Repo), "SK": s("OPENJOB#" + j.Key)}})
	return err
}

func fromJobItem(a jobAttrs) model.Job {
	return model.Job{Key: a.Key, Repo: a.Repo, Datasource: a.Datasource, Package: a.Package, Version: a.Version,
		ReleasedAt: parseTS(a.ReleasedAt), State: model.JobState(a.State), NotBefore: parseTS(a.NotBefore),
		Attempts: a.Attempts, PRURL: a.PRURL, Reason: a.Reason}
}

func (d *Dynamo) GetJob(ctx context.Context, key string) (model.Job, bool, error) {
	var a jobAttrs
	ok, err := d.get(ctx, "JOB#"+key, "META", &a)
	if err != nil || !ok {
		return model.Job{}, false, wrapD("get job", err)
	}
	return fromJobItem(a), true, nil
}

func (d *Dynamo) UpdateJob(ctx context.Context, j model.Job) error {
	err := d.put(ctx, d.jobItem(j), nil)
	if err == nil {
		err = d.syncOpenJob(ctx, j)
	}
	return wrapD("update job", err)
}

// ListDueJobs queries every pending-job shard for its first limit due items and merges
// them in (NotBefore, key) order, so the result is what one partition would give. It
// costs exactly jobShards Queries and reads at most jobShards*limit items.
func (d *Dynamo) ListDueJobs(ctx context.Context, now time.Time, limit int) ([]model.Job, error) {
	return d.ListDueJobsExcluding(ctx, now, limit, nil)
}

// ListDueJobsExcluding is ListDueJobs with a filter expression dropping the named repos'
// jobs. DynamoDB applies a filter after reading, so the excluded repos' due items are still
// read (a page of limit items at a time) and not billed back; the exclusion list is the
// handful of repos whose run is in flight.
func (d *Dynamo) ListDueJobsExcluding(ctx context.Context, now time.Time, limit int, repos []string) ([]model.Job, error) {
	if limit <= 0 {
		return nil, nil
	}
	vals := map[string]types.AttributeValue{}
	var filter *string
	var names map[string]string
	if len(repos) > 0 {
		var conds []string
		for i, r := range repos {
			k := fmt.Sprintf(":x%d", i)
			vals[k] = s(r)
			conds = append(conds, "#repo <> "+k)
		}
		filter, names = aws.String(strings.Join(conds, " AND ")), map[string]string{"#repo": "Repo"}
	}
	parts, err := fanOut(ctx, d.jobShards, d.concurrency, func(ctx context.Context, sh int) ([]model.Job, error) {
		var part []model.Job
		err := d.query(ctx, &dynamodb.QueryInput{
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk AND GSI1SK <= :max"),
			FilterExpression:          filter,
			ExpressionAttributeNames:  names,
			ExpressionAttributeValues: withVals(vals, map[string]types.AttributeValue{":pk": s(pendingJobsPK(sh)), ":max": s(ts(now) + sep + "\U0010ffff")}),
			Limit:                     aws.Int32(int32(limit)),
		}, limit, func(it map[string]types.AttributeValue) error {
			var a jobAttrs
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return err
			}
			part = append(part, fromJobItem(a))
			return nil
		})
		return part, err
	})
	if err != nil {
		return nil, wrapD("list due jobs", err)
	}
	return mergeDueJobs(parts, limit), nil
}

// withVals returns a fresh map holding both, so each shard's query owns its own.
func withVals(a, b map[string]types.AttributeValue) map[string]types.AttributeValue {
	out := make(map[string]types.AttributeValue, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (d *Dynamo) GetCursor(ctx context.Context, n string) (string, error) {
	var a struct{ Value string }
	_, err := d.get(ctx, "CURSOR#"+n, "META", &a)
	return a.Value, wrapD("get cursor", err)
}

func (d *Dynamo) SetCursor(ctx context.Context, n, v string) error {
	return wrapD("set cursor", d.put(ctx, cursorItem(n, v), nil))
}

func wrapD(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dynamodb %s: %w", op, err)
}

// UpdateJobFenced is store.Store. One TransactWriteItems checks the lease item, writes the
// job and writes or deletes its open-job copy, so a stale holder changes nothing, and the
// copy cannot fall behind the job as it can in UpdateJob.
func (d *Dynamo) UpdateJobFenced(ctx context.Context, j model.Job, l Lease) error {
	m, err := attributevalue.MarshalMap(d.jobItem(j))
	if err != nil {
		return wrapD("update job", err)
	}
	copyKey := map[string]types.AttributeValue{"PK": s("REPO#" + j.Repo), "SK": s("OPENJOB#" + j.Key)}
	var second types.TransactWriteItem
	if isOpen(j) {
		c := d.jobItem(j)
		c.item = item{PK: "REPO#" + j.Repo, SK: "OPENJOB#" + j.Key}
		cm, err := attributevalue.MarshalMap(c)
		if err != nil {
			return wrapD("update job", err)
		}
		second.Put = &types.Put{TableName: &d.table, Item: cm}
	} else {
		second.Delete = &types.Delete{TableName: &d.table, Key: copyKey}
	}
	_, err = d.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{TableName: &d.table, Key: leaseKey(l.Name),
			ConditionExpression:       aws.String("#o = :o AND #t = :t"),
			ExpressionAttributeNames:  map[string]string{"#o": "Owner", "#t": "Token"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":o": s(l.Owner), ":t": &types.AttributeValueMemberN{Value: fmt.Sprint(l.Token)}}}},
		{Put: &types.Put{TableName: &d.table, Item: m}},
		second,
	}})
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) {
		return ErrLeaseLost
	}
	return wrapD("update job", err)
}
