package store

import (
	"context"
	"errors"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// CountDrift is one package whose dependents counter disagrees with its index.
type CountDrift struct {
	Base    string // PKG#<datasource>#<line>
	Indexed int    // dependents found in the index
	Counted int    // the counter's sum (0 when it has none)
}

// scanItems runs a filtered, projected Scan over the whole table and passes each item on.
// It reads every item, so it belongs to operator commands, never to a request path.
func (d *Dynamo) scanItems(ctx context.Context, filter, projection string, names map[string]string, vals map[string]types.AttributeValue, fn func(map[string]types.AttributeValue) error) error {
	in := &dynamodb.ScanInput{TableName: &d.table, ConsistentRead: aws.Bool(true),
		FilterExpression: aws.String(filter), ExpressionAttributeNames: names, ExpressionAttributeValues: vals}
	if projection != "" {
		in.ProjectionExpression = aws.String(projection)
	}
	for {
		out, err := d.db.Scan(ctx, in)
		if err != nil {
			return err
		}
		for _, it := range out.Items {
			if err := fn(it); err != nil {
				return err
			}
		}
		if out.LastEvaluatedKey == nil {
			return nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// dependentBases lists every package that has at least one dependent, by scanning the
// dependency items. A package whose counter was never written is found this way too.
func (d *Dynamo) dependentBases(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	err := d.scanItems(ctx, "begins_with(SK, :dep)", "SK", nil,
		map[string]types.AttributeValue{":dep": s("DEP#")}, func(it map[string]types.AttributeValue) error {
			if sk, _ := it["SK"].(*types.AttributeValueMemberS); sk != nil {
				if b := depBaseOf(sk.Value); b != "" {
					seen[b] = true
				}
			}
			return nil
		})
	out := make([]string, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Strings(out)
	return out, err
}

// indexedDependents counts base's dependents across its shards without reading them.
func (d *Dynamo) indexedDependents(ctx context.Context, base string) (int, error) {
	shards, err := d.depShardsFor(ctx, base, true)
	if err != nil {
		return 0, err
	}
	n := 0
	for sh := 0; sh < shards; sh++ {
		err := d.query(ctx, &dynamodb.QueryInput{
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk"),
			ProjectionExpression:      aws.String("PK"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(dependentsPK(base, sh))},
		}, 0, func(map[string]types.AttributeValue) error { n++; return nil })
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

// CheckDependentCounts compares every package's counter with its index and returns the
// packages that differ, in package order. It writes nothing. It scans the whole table.
func (d *Dynamo) CheckDependentCounts(ctx context.Context) ([]CountDrift, error) {
	bases, err := d.dependentBases(ctx)
	if err != nil {
		return nil, wrapD("check dependent counts", err)
	}
	var out []CountDrift
	for _, b := range bases {
		idx, err := d.indexedDependents(ctx, b)
		if err != nil {
			return nil, wrapD("check dependent counts", err)
		}
		_, _, sum, err := d.counterItems(ctx, b)
		if err != nil {
			return nil, wrapD("check dependent counts", err)
		}
		if idx != sum {
			out = append(out, CountDrift{Base: b, Indexed: idx, Counted: sum})
		}
	}
	return out, nil
}

// RecountAllDependents runs RecountDependents for every package that has dependents and
// returns how many it recounted. It is the one-off for a table whose dependents predate the
// option, or after a crash left counters drifted; run it from an operator command, not on
// start. It scans the whole table and needs WithAdaptiveDependents.
func (d *Dynamo) RecountAllDependents(ctx context.Context) (int, error) {
	if d.adaptive <= 0 {
		return 0, errors.New("dynamodb recount dependents: needs WithAdaptiveDependents")
	}
	bases, err := d.dependentBases(ctx)
	if err != nil {
		return 0, wrapD("recount dependents", err)
	}
	for i, b := range bases {
		if _, err := d.recountBase(ctx, b); err != nil {
			return i, wrapD("recount dependents", err)
		}
	}
	return len(bases), nil
}

// BackfillFailedJobs writes the open-job copy of every failed job that lacks one and
// returns how many it wrote. Pending jobs are backfilled when the table is opened, from
// the pending index; failed jobs are not in it, so only a scan finds them. It is a
// one-off for a table from before the copies existed and is safe to repeat.
func (d *Dynamo) BackfillFailedJobs(ctx context.Context) (int, error) {
	var failed []jobAttrs
	err := d.scanItems(ctx, "begins_with(PK, :job) AND SK = :meta AND #s = :failed", "",
		map[string]string{"#s": "State"},
		map[string]types.AttributeValue{":job": s("JOB#"), ":meta": s("META"), ":failed": s("failed")},
		func(it map[string]types.AttributeValue) error {
			var a jobAttrs
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return err
			}
			failed = append(failed, a)
			return nil
		})
	if err != nil {
		return 0, wrapD("backfill failed jobs", err)
	}
	n, err := d.backfillCopies(ctx, failed)
	return n, wrapD("backfill failed jobs", err)
}
