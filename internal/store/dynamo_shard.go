package store

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand/v2"
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
	sk, _, _ = strings.Cut(sk, depVersionSep)
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

// RecountDependents recounts one package's dependents from the index, rewrites the counter
// to the truth, raises the recorded shard count if the count calls for it, and moves every
// dependent to the shard its repo hashes to. It returns the count. It is the repair for a
// counter that drifted (the counter is not transactional with the item writes) and for a
// table whose dependents predate the option, and is safe to repeat and to run beside
// writers: a move only applies to an item still where it was read, and a writer that races
// the final count leaves drift the next recount removes. It needs WithAdaptiveDependents.
func (d *Dynamo) RecountDependents(ctx context.Context, ds, pkg string) (int, error) {
	if d.adaptive <= 0 {
		return 0, errors.New("dynamodb recount dependents: needs WithAdaptiveDependents")
	}
	n, err := d.recountBase(ctx, pkgBase(ds, pkg))
	return n, wrapD("recount dependents", err)
}

func (d *Dynamo) recountBase(ctx context.Context, base string) (int, error) {
	shards, err := d.depShardsFor(ctx, base, true)
	if err != nil {
		return 0, err
	}
	type found struct{ pk, sk, gsi, repo string }
	var all []found
	for sh := 0; sh < shards; sh++ {
		err := d.query(ctx, &dynamodb.QueryInput{
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk"),
			ProjectionExpression:      aws.String("PK, SK, GSI1PK, Repo"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(dependentsPK(base, sh))},
		}, 0, func(it map[string]types.AttributeValue) error {
			str := func(k string) string {
				v, _ := it[k].(*types.AttributeValueMemberS)
				if v == nil {
					return ""
				}
				return v.Value
			}
			all = append(all, found{str("PK"), str("SK"), str("GSI1PK"), str("Repo")})
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	count := len(all)
	want := max(shards, targetShards(count, d.adaptive))
	counter := map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s("META")}
	// Raise the shard count before moving anything, so a reader that sees a moved item
	// already reads the shard it moved to.
	if want > 1 {
		if err := d.raiseShards(ctx, base, want); err != nil {
			return 0, err
		}
	}
	d.pkgShards.Store(base, want)
	for _, f := range all {
		to := dependentsPK(base, shardOf(f.repo, want))
		if to == f.gsi {
			continue
		}
		_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(f.pk), "SK": s(f.sk)},
			UpdateExpression:          aws.String("SET GSI1PK = :to"),
			ConditionExpression:       aws.String("GSI1PK = :from"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":to": s(to), ":from": s(f.gsi)},
		})
		var cc *types.ConditionalCheckFailedException
		if err != nil && !errors.As(err, &cc) {
			return 0, err
		}
	}
	stripes, _, _, err := d.counterItems(ctx, base)
	if err != nil {
		return 0, err
	}
	for _, sk := range stripes {
		if _, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s(sk)},
			UpdateExpression:          aws.String("SET Deps = :z"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":z": &types.AttributeValueMemberN{Value: "0"}},
		}); err != nil {
			return 0, err
		}
	}
	_, err = d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table, Key: counter,
		UpdateExpression:          aws.String("SET Deps = :c"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":c": &types.AttributeValueMemberN{Value: strconv.Itoa(count)}},
	})
	return count, err
}

// adjustCounts applies per-package dependent-count deltas and grows a package's recorded
// shard count when the rule asks for more. It runs once the table has ever had the option
// on, including while it is off: the counters keep moving (the rule gives no growth with
// no per-shard size), so switching it back on starts from counts that are still true.
// A table that never had it on keeps none.
func (d *Dynamo) adjustCounts(ctx context.Context, delta map[string]int) error {
	if !d.adaptiveOn() {
		return nil
	}
	for base, dl := range delta {
		if dl == 0 || base == "" {
			continue
		}
		if d.stripes > 1 {
			if err := d.adjustStriped(ctx, base, dl); err != nil {
				return err
			}
			continue
		}
		out, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s("META")},
			UpdateExpression:          aws.String("ADD Deps :d"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":d": &types.AttributeValueMemberN{Value: strconv.Itoa(dl)}},
			ReturnValues:              types.ReturnValueUpdatedOld,
		})
		if err != nil {
			return err
		}
		old, had := out.Attributes["Deps"].(*types.AttributeValueMemberN)
		if !had {
			// This ADD created the counter, so it started from zero while dependents may already
			// exist (written before the option was on, or before the counter was lost). Count them.
			if _, err := d.recountBase(ctx, base); err != nil {
				return err
			}
			continue
		}
		prev, _ := strconv.Atoi(old.Value)
		n := prev + dl
		want := targetShards(n, d.adaptive)
		if want <= 1 {
			continue
		}
		if cur, ok := d.pkgShards.Load(base); ok && cur.(int) >= want {
			continue
		}
		if err := d.raiseShards(ctx, base, want); err != nil {
			return err
		}
	}
	return nil
}

// raiseShards records want as base's shard count unless the table already holds more, and
// remembers it. It never lowers: the count is a high-water mark.
func (d *Dynamo) raiseShards(ctx context.Context, base string, want int) error {
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
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
	return nil
}

// counterItems reads base's counter partition: the sort keys of its stripe items (S#<i>),
// the number of items of any kind, and the sum of their Deps. One Query on one partition, so
// it stays cheap while the stripes are few.
func (d *Dynamo) counterItems(ctx context.Context, base string) (stripes []string, items, sum int, err error) {
	err = d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ProjectionExpression:      aws.String("SK, Deps"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pkgCountPK(base))},
	}, 0, func(it map[string]types.AttributeValue) error {
		items++
		if sk, _ := it["SK"].(*types.AttributeValueMemberS); sk != nil && strings.HasPrefix(sk.Value, "S#") {
			stripes = append(stripes, sk.Value)
		}
		if v, _ := it["Deps"].(*types.AttributeValueMemberN); v != nil {
			n, _ := strconv.Atoi(v.Value)
			sum += n
		}
		return nil
	})
	return stripes, items, sum, err
}

// adjustStriped is adjustCounts for one package with striped counters: the ADD lands on a
// random stripe, so concurrent writers do not share an item. A stripe sees about 1/stripes
// of the count, so the stripe's own value scaled up is a cheap estimate; only when that
// estimate says the package may need more shards is the partition summed for the exact count.
// The estimate can lag the truth, but every later write re-checks, so growth is delayed, not lost.
func (d *Dynamo) adjustStriped(ctx context.Context, base string, dl int) error {
	out, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(pkgCountPK(base)), "SK": s("S#" + strconv.Itoa(rand.IntN(d.stripes)))},
		UpdateExpression:          aws.String("ADD Deps :d"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":d": &types.AttributeValueMemberN{Value: strconv.Itoa(dl)}},
		ReturnValues:              types.ReturnValueUpdatedOld,
	})
	if err != nil {
		return err
	}
	var total int
	if old, had := out.Attributes["Deps"].(*types.AttributeValueMemberN); had {
		prev, _ := strconv.Atoi(old.Value)
		cur := 1
		if v, ok := d.pkgShards.Load(base); ok {
			cur = v.(int)
		}
		if targetShards((prev+dl)*d.stripes, d.adaptive) <= cur {
			return nil
		}
		if _, _, total, err = d.counterItems(ctx, base); err != nil {
			return err
		}
	} else {
		// This stripe item is new. If it is the only item in the partition the counter did not
		// exist (see adjustCounts); otherwise another stripe or the unstriped item holds the count.
		var items int
		if _, items, total, err = d.counterItems(ctx, base); err != nil {
			return err
		}
		if items == 1 {
			_, err = d.recountBase(ctx, base)
			return err
		}
	}
	if want := targetShards(total, d.adaptive); want > 1 {
		return d.raiseShards(ctx, base, want)
	}
	return nil
}
