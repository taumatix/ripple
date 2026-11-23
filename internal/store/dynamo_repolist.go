package store

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// The repo listing (store.RepoLister). DynamoDB has no ordered listing of items by
// prefix across partitions, so each repo gets one index item (PK=REPOS, SK=<full name>) written
// beside its META item and removed with it; ListRepos is a Query on that one partition.
// A repo stored before the index existed has none, so the first listing runs one Scan of
// the table for META items, writes the missing index items and then a marker
// (PK=REPOSMETA, SK=BUILT). The Scan runs once per table, only for a store that is asked to
// list (the advisory sweep after a gap), never on a scan or a webhook.
const (
	repoIndexPK  = "REPOS"
	repoMarkerPK = "REPOSMETA"
)

type repoIndexAttrs struct {
	item
}

func (d *Dynamo) putRepoIndex(ctx context.Context, name string) error {
	return d.put(ctx, repoIndexAttrs{item{PK: repoIndexPK, SK: name}}, nil)
}

func (d *Dynamo) deleteRepoIndex(ctx context.Context, name string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &d.table,
		Key: map[string]types.AttributeValue{"PK": s(repoIndexPK), "SK": s(name)}})
	return err
}

// ListRepos is store.RepoLister.
func (d *Dynamo) ListRepos(ctx context.Context, after string, limit int) ([]string, error) {
	if err := d.ensureRepoIndex(ctx); err != nil {
		return nil, wrapD("list repos", err)
	}
	if limit <= 0 {
		return nil, nil
	}
	var out []string
	// DynamoDB refuses an empty key value, so the first page has no SK bound.
	cond, vals := "PK = :pk", map[string]types.AttributeValue{":pk": s(repoIndexPK)}
	if after != "" {
		cond, vals[":after"] = cond+" AND SK > :after", s(after)
	}
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String(cond),
		ExpressionAttributeValues: vals,
		Limit:                     aws.Int32(int32(limit)),
	}, limit, func(it map[string]types.AttributeValue) error {
		out = append(out, it["SK"].(*types.AttributeValueMemberS).Value)
		return nil
	})
	return out, wrapD("list repos", err)
}

// ensureRepoIndex indexes the repos stored before the index existed, once.
func (d *Dynamo) ensureRepoIndex(ctx context.Context) error {
	var m item
	if ok, err := d.get(ctx, repoMarkerPK, "BUILT", &m); err != nil || ok {
		return err
	}
	in := &dynamodb.ScanInput{TableName: &d.table, ConsistentRead: aws.Bool(true),
		FilterExpression:          aws.String("SK = :meta AND begins_with(PK, :repo)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":meta": s("META"), ":repo": s("REPO#")},
		ProjectionExpression:      aws.String("PK, SK, full_name")}
	for {
		out, err := d.db.Scan(ctx, in)
		if err != nil {
			return err
		}
		for _, it := range out.Items {
			var a repoAttrs
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return err
			}
			if a.FullName == "" {
				continue
			}
			if err := d.putRepoIndex(ctx, a.FullName); err != nil {
				return err
			}
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return d.put(ctx, item{PK: repoMarkerPK, SK: "BUILT"}, nil)
}
