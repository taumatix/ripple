package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Handled webhook deliveries are stored as
//
//	delivery  PK=DELIVERY#<key>  SK=META  GSI1PK=DELIVERIES  GSI1SK=<expiry>\x1f<key>  expires_at=<expiry>
//
// A claim is one conditional PutItem (absent, or expired). PurgeDeliveries reads the key
// range of the one GSI partition before the cut-off, the way PurgeDeadEvents does; that
// partition sees one write per webhook delivery, far below its throughput limit.
const deliveriesPK = "DELIVERIES"

type deliveryAttrs struct {
	item
	ExpiresAt string `dynamodbav:"expires_at"`
}

// ClaimDelivery is store.DeliveryTable.
func (d *Dynamo) ClaimDelivery(ctx context.Context, key string, now time.Time, ttl time.Duration) (bool, error) {
	exp := ts(now.Add(ttl))
	err := d.putCond(ctx, deliveryAttrs{
		item:      item{PK: "DELIVERY#" + key, SK: "META", GSI1PK: deliveriesPK, GSI1SK: exp + sep + key},
		ExpiresAt: exp,
	}, "attribute_not_exists(PK) OR expires_at <= :now", map[string]types.AttributeValue{":now": s(ts(now))})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return false, nil
	}
	return err == nil, wrapD("claim delivery", err)
}

// ReleaseDelivery is store.DeliveryTable.
func (d *Dynamo) ReleaseDelivery(ctx context.Context, key string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key:       map[string]types.AttributeValue{"PK": s("DELIVERY#" + key), "SK": s("META")},
	})
	return wrapD("release delivery", err)
}

// PurgeDeliveries is store.DeliveryTable. The index is eventually consistent: a key
// claimed a moment ago is listed at the next purge, and each delete is conditional on the
// item still being expired, so a key re-claimed since the query stays.
func (d *Dynamo) PurgeDeliveries(ctx context.Context, before time.Time) (int, error) {
	var keys []string
	err := d.query(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(gsi1),
		KeyConditionExpression:    aws.String("GSI1PK = :pk AND GSI1SK < :max"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(deliveriesPK), ":max": s(ts(before))},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a deliveryAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		keys = append(keys, strings.TrimPrefix(a.PK, "DELIVERY#"))
		return nil
	})
	if err != nil {
		return 0, wrapD("purge deliveries", err)
	}
	n := 0
	for _, k := range keys {
		_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:                 &d.table,
			Key:                       map[string]types.AttributeValue{"PK": s("DELIVERY#" + k), "SK": s("META")},
			ConditionExpression:       aws.String("expires_at < :max"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":max": s(ts(before))},
		})
		var cc *types.ConditionalCheckFailedException
		switch {
		case errors.As(err, &cc):
		case err != nil:
			return n, wrapD("purge deliveries", err)
		default:
			n++
		}
	}
	return n, nil
}

// PurgeCursors is store.CursorPurger. A prefix with a group ("removed#") lists the group's
// gsi1 partition with a Query, which is how the removal tombstones are swept; any other
// prefix is a filtered Scan of the whole table, because DynamoDB cannot list items by
// partition-key prefix, so callers run it rarely. Each delete is conditional on the value
// read, and a cursor gsi1 has not caught up with yet is simply swept on the next run.
func (d *Dynamo) PurgeCursors(ctx context.Context, prefix string, drop func(string) bool) (int, error) {
	type cur struct{ pk, value string }
	var drops []cur
	listed, err := d.ListCursors(ctx, prefix)
	if err != nil {
		return 0, err
	}
	for _, c := range listed {
		if drop(c.Value) {
			drops = append(drops, cur{"CURSOR#" + c.Name, c.Value})
		}
	}
	n := 0
	for _, c := range drops {
		_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:                 &d.table,
			Key:                       map[string]types.AttributeValue{"PK": s(c.pk), "SK": s("META")},
			ConditionExpression:       aws.String("#v = :v"),
			ExpressionAttributeNames:  map[string]string{"#v": "Value"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":v": s(c.value)},
		})
		var cc *types.ConditionalCheckFailedException
		switch {
		case errors.As(err, &cc):
		case err != nil:
			return n, wrapD("purge cursors", err)
		default:
			n++
		}
	}
	return n, nil
}
