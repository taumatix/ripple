package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Live replicas are stored as
//
//	replica  PK=REPLICAS  SK=R#<id>  Takeovers  FeedPoller  Seen  Expires
//
// in one base-table partition, so LiveReplicas is a consistent Query without an index; the
// partition takes one write per replica per beat, a handful per lease ttl. Times are the
// replica's wall clock, as for DynamoDB leases.
const replicasPK = "REPLICAS"

type replicaAttrs struct {
	item
	Takeovers     int64
	FeedPoller    bool
	Seen, Expires string
}

// Heartbeat is store.ReplicaTable.
func (d *Dynamo) Heartbeat(ctx context.Context, r ReplicaInfo, ttl time.Duration) error {
	now := time.Now()
	return wrapD("heartbeat", d.put(ctx, replicaAttrs{
		item:      item{PK: replicasPK, SK: "R#" + r.ID},
		Takeovers: r.Takeovers, FeedPoller: r.FeedPoller, Seen: ts(now), Expires: ts(now.Add(ttl)),
	}, nil))
}

// LiveReplicas is store.ReplicaTable.
func (d *Dynamo) LiveReplicas(ctx context.Context) ([]ReplicaInfo, error) {
	now := time.Now()
	var out []ReplicaInfo
	var stale []string
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(replicasPK)},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a replicaAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		exp := parseTS(a.Expires)
		switch {
		case exp.After(now):
			out = append(out, ReplicaInfo{ID: strings.TrimPrefix(a.SK, "R#"), Takeovers: a.Takeovers, FeedPoller: a.FeedPoller, Seen: parseTS(a.Seen)})
		case now.Sub(exp) > replicaGrace:
			stale = append(stale, a.SK)
		}
		return nil
	})
	if err != nil {
		return nil, wrapD("live replicas", err)
	}
	for _, sk := range stale { // conditional: a replica that beat since the query stays
		_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(replicasPK), "SK": s(sk)},
			ConditionExpression:       aws.String("#e < :max"),
			ExpressionAttributeNames:  map[string]string{"#e": "Expires"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":max": s(ts(now.Add(-replicaGrace)))},
		})
		var cc *types.ConditionalCheckFailedException
		if err != nil && !errors.As(err, &cc) {
			return nil, wrapD("live replicas", err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RetireReplica is store.ReplicaTable.
func (d *Dynamo) RetireReplica(ctx context.Context, id string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s(replicasPK), "SK": s("R#" + id)},
	})
	return wrapD("retire replica", err)
}
