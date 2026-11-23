package store

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// Sharding spreads the two GSI partitions that concentrate traffic: the pending-job
// queue (every due-job poll and every enqueue) and a popular package's dependents.
//
// Shard 0 uses the key the table has always used (JOBS#pending, PKG#<ds>#<line>), so a
// table written before sharding needs no migration: its items are read as shard 0 and
// move to their proper shard the next time they are written (a job on its next
// UpdateJob, a repo's dependencies on its next scan).
const (
	// DefaultJobShards spreads the pending queue; a due-jobs poll costs this many Queries.
	DefaultJobShards = 8
	// DefaultDependentShards keeps one partition per package: ListDependents is the
	// hot read path and each shard multiplies its cost. Raise it only for a table where
	// one package is imported by so many repos that its partition throttles.
	DefaultDependentShards = 1
	// DefaultQueryConcurrency bounds the shard Queries one read has in flight.
	DefaultQueryConcurrency = 4
	// MaxShards bounds the fan-out of any one read.
	MaxShards = 64
)

func clampShards(n int) int { return min(max(n, 1), MaxShards) }

func shardOf(key string, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

func pendingJobsPK(shard int) string {
	if shard == 0 {
		return "JOBS#pending"
	}
	return "JOBS#pending#" + strconv.Itoa(shard)
}

// dependentsPK is the GSI partition of a package's dependents; base is PKG#<ds>#<line>.
// Package names cannot contain the separator, so a suffixed key never collides.
func dependentsPK(base string, shard int) string {
	if shard == 0 {
		return base
	}
	return base + sep + strconv.Itoa(shard)
}

// mergeDueJobs merges per-shard results, each already in index order, into one list in
// the same order a single partition would give (NotBefore, then key), cut to limit.
// Each shard was asked for limit items, so the first limit of the union is complete.
func mergeDueJobs(parts [][]model.Job, limit int) []model.Job {
	var all []model.Job
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.SliceStable(all, func(i, k int) bool {
		a, b := ts(all[i].NotBefore)+sep+all[i].Key, ts(all[k].NotBefore)+sep+all[k].Key
		return a < b
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}

// fanOut runs fn for shards 0..n-1, at most bound at a time, and returns the results in
// shard order whatever order they finish in, so a merge over them is deterministic. The
// first error cancels the context handed to the others and is the one returned; a
// cancelled parent context stops launching and returns its error. It does not return
// until every started fn has.
func fanOut[T any](parent context.Context, n, bound int, fn func(ctx context.Context, shard int) (T, error)) ([]T, error) {
	out := make([]T, n)
	if bound <= 1 || n <= 1 {
		for i := 0; i < n; i++ {
			if err := parent.Err(); err != nil {
				return nil, err
			}
			v, err := fn(parent, i)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
		sem      = make(chan struct{}, bound)
	)
launch:
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break launch
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := fn(ctx, i)
			if err != nil {
				once.Do(func() { firstErr = err; cancel() })
				return
			}
			out[i] = v
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Adaptive dependent sharding.
//
// With WithAdaptiveDependents(k) a package's dependents start in one partition (the key
// the table has always used) and the package's shard count doubles each time its dependent
// count exceeds k per shard: n = 1; while count > k*n { n *= 2 }, capped at MaxShards.
// The count is a counter item (PKGCOUNT#<ds>#<line>) moved with ADD by ReplaceDependencies
// and DeleteRepo; the shard count is recorded in the same item and only ever grows, like
// the global high-water mark. The effective count for a package is
// max(WithDependentShards, recorded), and the CONFIG#shards item remembers that adaptive
// sharding was ever on, so turning the option off later still reads every shard.
//
// Growing never loses or duplicates a read. A dependency item has exactly one GSI1PK, and
// items written under a lower count sit in a shard below the new count, which reads cover.
// A rewrite under the new count replaces the item in place (same table key), moving it.
// The counter only steers growth: it is not transactional with the item writes, so it can
// drift after a crash, and dependents that existed before the option was turned on are
// not counted until they are rewritten. Neither affects read correctness.

// targetShards is the shard count the adaptive rule gives a package with count dependents.
func targetShards(count, perShard int) int {
	n := 1
	for perShard > 0 && count > perShard*n && n < MaxShards {
		n *= 2
	}
	return n
}

func pkgCountPK(base string) string { return "PKGCOUNT#" + base }

// depBaseOf recovers PKG#<ds>#<line> from a dependency sort key DEP#<file>\x1f<ds>\x1f<pkg>.
func depBaseOf(sk string) string {
	parts := strings.Split(sk, sep)
	if len(parts) < 3 {
		return ""
	}
	ds, pkg := parts[len(parts)-2], parts[len(parts)-1]
	return pkgBase(ds, pkg)
}

// depShardsFor is how many GSI partitions hold base's dependents. Reads pass fresh=true
// and always consult the table; the write path may use a cached (possibly stale, never too
// high) value, which is safe because a lower count only places items in lower shards.
func (d *Dynamo) depShardsFor(ctx context.Context, base string, fresh bool) (int, error) {
	if !d.adaptiveOn() {
		return d.depShards, nil
	}
	if !fresh {
		if v, ok := d.pkgShards.Load(base); ok {
			return max(d.depShards, v.(int)), nil
		}
	}
	var a struct {
		item
		Deps, Shards int
	}
	ok, err := d.get(ctx, pkgCountPK(base), "META", &a)
	if err != nil {
		return 0, err
	}
	rec := 1
	if ok {
		rec = clampShards(a.Shards)
	}
	d.pkgShards.Store(base, rec)
	return max(d.depShards, rec), nil
}

func (d *Dynamo) adaptiveOn() bool { return d.adaptive > 0 || d.adaptiveSeen }

// adjustCounts applies per-package dependent-count deltas and grows a package's recorded
// shard count when the rule asks for more. It only runs with the option on.
func (d *Dynamo) adjustCounts(ctx context.Context, delta map[string]int) error {
	if d.adaptive <= 0 {
		return nil
	}
	for base, dl := range delta {
		if dl == 0 || base == "" {
			continue
		}
		out, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s("META")},
			UpdateExpression:          aws.String("ADD Deps :d"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":d": &types.AttributeValueMemberN{Value: strconv.Itoa(dl)}},
			ReturnValues:              types.ReturnValueUpdatedNew,
		})
		if err != nil {
			return err
		}
		n, _ := strconv.Atoi(out.Attributes["Deps"].(*types.AttributeValueMemberN).Value)
		want := targetShards(n, d.adaptive)
		if want <= 1 {
			continue
		}
		if cur, ok := d.pkgShards.Load(base); ok && cur.(int) >= want {
			continue
		}
		_, err = d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s("META")},
			UpdateExpression:          aws.String("SET Shards = :n"),
			ConditionExpression:       aws.String("attribute_not_exists(Shards) OR Shards < :n"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":n": &types.AttributeValueMemberN{Value: strconv.Itoa(want)}},
		})
		var cc *types.ConditionalCheckFailedException
		if err != nil && !errors.As(err, &cc) {
			return err
		}
		d.pkgShards.Store(base, want)
	}
	return nil
}
