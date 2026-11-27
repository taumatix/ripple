package store

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Recent registrations, the consistent half of ListDependents (ROADMAP entry 120).
//
// A global secondary index cannot be read consistently, so a repo that registered a dependency
// a moment before that package released could be missed by the release's fan-out. When
// ReplaceDependencies adds a dependency row it also writes a registration item into the
// package's base-table partition (PK=FRESH#<package base>, SK=<repo>\x1f<dependency SK>),
// stamped with the time. ListDependents adds, to what the index returns, the rows those items
// from the last freshDepWindow name, read with strongly consistent BatchGetItems: the row
// itself is the truth, so a dependency dropped or re-versioned since is reported as it now is.
// An item older than the window is deleted by the read that finds it. Only a row new to the
// repo writes an item, so a steady-state rescan costs nothing extra; tables written before
// this have no items and read as before.
const (
	freshDepWindow = 5 * time.Minute
	freshPKPrefix  = "FRESH#"
)

type freshAttrs struct {
	item
	Repo  string
	DepSK string
	At    string
}

func freshItem(base, repo, depSK string, at time.Time) freshAttrs {
	return freshAttrs{item: item{PK: freshPKPrefix + base, SK: repo + sep + depSK}, Repo: repo, DepSK: depSK, At: ts(at)}
}

// freshDependents reads the rows registered under base inside the window.
func (d *Dynamo) freshDependents(ctx context.Context, base string) ([]depAttrs, error) {
	cutoff := ts(d.now().Add(-freshDepWindow))
	var keys, stale []map[string]types.AttributeValue
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(freshPKPrefix + base)},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a freshAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		if a.At < cutoff {
			stale = append(stale, map[string]types.AttributeValue{"PK": s(a.PK), "SK": s(a.SK)})
			return nil
		}
		keys = append(keys, map[string]types.AttributeValue{"PK": s("REPO#" + a.Repo), "SK": s(a.DepSK)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(stale) > 0 {
		var reqs []types.WriteRequest
		for _, k := range stale {
			reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: k}})
		}
		_ = d.batch(ctx, reqs) // housekeeping: the next read tries again
	}
	items, err := d.batchGetItems(ctx, keys)
	if err != nil {
		return nil, err
	}
	var out []depAttrs
	for _, it := range items {
		var a depAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// batchGetItems reads keys with strongly consistent BatchGetItems of at most 100, retrying
// UnprocessedKeys. An absent item is not in the result.
func (d *Dynamo) batchGetItems(ctx context.Context, keys []map[string]types.AttributeValue) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	for len(keys) > 0 {
		pending := keys[:min(batchGetMax, len(keys))]
		keys = keys[len(pending):]
		for attempt := 0; len(pending) > 0; attempt++ {
			r, err := d.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: map[string]types.KeysAndAttributes{
				d.table: {Keys: pending, ConsistentRead: aws.Bool(true)}}})
			if err != nil {
				return nil, err
			}
			out = append(out, r.Responses[d.table]...)
			pending = r.UnprocessedKeys[d.table].Keys
			if len(pending) > 0 {
				if attempt > 8 {
					return nil, errors.New("batch get: unprocessed keys remain after retries")
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
				}
			}
		}
	}
	return out, nil
}
