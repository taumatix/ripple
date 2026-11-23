package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// A live event has a second, base-table item, its marker:
//
//	marker  PK=REPOEVENTS#<repo>  SK=<seq, 20 digits>\x1f<id>  ID=<id>
//
// It exists exactly while the event is live: written in the transaction that enqueues or
// requeues the event, deleted in the one that dead-letters or deletes it. HasOlderEvent reads
// a repo's markers with a consistent Query, which the eventually consistent gsi1 cannot do
// (DynamoDB refuses ConsistentRead on an index). Events written before markers existed get
// theirs from one backfill (eventMarksBuilt), and a marker an older replica left behind is
// removed when HasOlderEvent finds its event gone.
const (
	repoEventsPrefix = "REPOEVENTS#"
	eventMarksPK     = "EVENTMARKS"
)

type eventMarkAttrs struct {
	item
	ID string
}

func markerSK(e model.Event) string { return fmt.Sprintf("%020d", e.Seq) + sep + e.ID }

func markerKey(e model.Event) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s(repoEventsPrefix + e.Repo), "SK": s(markerSK(e))}
}

func (d *Dynamo) markerPut(e model.Event) (types.TransactWriteItem, error) {
	m, err := attributevalue.MarshalMap(eventMarkAttrs{item: item{PK: repoEventsPrefix + e.Repo, SK: markerSK(e)}, ID: e.ID})
	return types.TransactWriteItem{Put: &types.Put{TableName: &d.table, Item: m}}, err
}

func (d *Dynamo) markerDelete(e model.Event) types.TransactWriteItem {
	return types.TransactWriteItem{Delete: &types.Delete{TableName: &d.table, Key: markerKey(e)}}
}

// eventWrites is the transaction items that write e's item (with cond) and keep its marker:
// present while e is live, absent once it is dead.
func (d *Dynamo) eventWrites(e model.Event, cond *string, vals map[string]types.AttributeValue) ([]types.TransactWriteItem, error) {
	m, err := attributevalue.MarshalMap(eventItem(e))
	if err != nil {
		return nil, err
	}
	out := []types.TransactWriteItem{{Put: &types.Put{TableName: &d.table, Item: m, ConditionExpression: cond, ExpressionAttributeValues: vals}}}
	if e.Dead() {
		return append(out, d.markerDelete(e)), nil
	}
	mk, err := d.markerPut(e)
	return append(out, mk), err
}

// transactEvent runs ws; a cancellation by a failed condition is (false, nil), any other
// cancellation (a conflicting transaction, throttling) is an error the caller retries.
func (d *Dynamo) transactEvent(ctx context.Context, ws []types.TransactWriteItem) (bool, error) {
	_, err := d.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: ws})
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) {
		for _, r := range tc.CancellationReasons {
			if aws.ToString(r.Code) == "ConditionalCheckFailed" {
				return false, nil
			}
		}
	}
	return err == nil, err
}

// deleteEventWrites deletes event id and its marker; with no such event it deletes nothing.
func (d *Dynamo) deleteEventWrites(ctx context.Context, id string) ([]types.TransactWriteItem, error) {
	var a eventAttrs
	ok, err := d.get(ctx, "EVENT#"+id, "META", &a)
	if err != nil || !ok {
		return nil, err
	}
	e := fromEventAttrs(a)
	ws := []types.TransactWriteItem{{Delete: &types.Delete{TableName: &d.table, Key: map[string]types.AttributeValue{"PK": s("EVENT#" + id), "SK": s("META")}}}}
	if !e.Dead() {
		ws = append(ws, d.markerDelete(e))
	}
	return ws, nil
}

// ensureEventMarks writes the markers of the live events that predate them, once per table
// (the BUILT item) and once per process. It reads the index, so an event written a moment
// before, by a replica that does not write markers yet, can be missed: roll every replica to
// this version before relying on the check.
func (d *Dynamo) ensureEventMarks(ctx context.Context) error {
	if d.marksBuilt.Load() {
		return nil
	}
	var built struct{ item }
	if ok, err := d.get(ctx, eventMarksPK, "BUILT", &built); err != nil {
		return err
	} else if ok {
		d.marksBuilt.Store(true)
		return nil
	}
	err := d.query(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(gsi1),
		KeyConditionExpression:    aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("EVENTS")},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a eventAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		e := fromEventAttrs(a)
		mk, err := d.markerPut(e)
		if err != nil {
			return err
		}
		// The event must still be live: a stale index entry must not resurrect a marker.
		check := types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{TableName: &d.table,
			Key:                       map[string]types.AttributeValue{"PK": s("EVENT#" + e.ID), "SK": s("META")},
			ConditionExpression:       aws.String("GSI1PK = :live"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":live": s("EVENTS")}}}
		_, err = d.transactEvent(ctx, []types.TransactWriteItem{check, mk})
		return err
	})
	if err != nil {
		return err
	}
	if err := d.put(ctx, struct{ item }{item{PK: eventMarksPK, SK: "BUILT"}}, nil); err != nil {
		return err
	}
	d.marksBuilt.Store(true)
	return nil
}

var errFoundOlder = errors.New("found an older event")

// HasOlderEvent is store.Store. It is a consistent Query of the repo's markers below e's
// key. A marker whose event is gone or dead (left by a replica from before markers) is
// deleted on the way and does not count.
func (d *Dynamo) HasOlderEvent(ctx context.Context, e model.Event) (bool, error) {
	if err := d.ensureEventMarks(ctx); err != nil {
		return false, wrapD("has older event", err)
	}
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk AND SK < :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(repoEventsPrefix + e.Repo), ":sk": s(markerSK(e))},
	}, 0, func(it map[string]types.AttributeValue) error {
		var m eventMarkAttrs
		if err := attributevalue.UnmarshalMap(it, &m); err != nil {
			return err
		}
		var a eventAttrs
		ok, err := d.get(ctx, "EVENT#"+m.ID, "META", &a)
		if err != nil {
			return err
		}
		if ok && a.DeadAt == "" {
			return errFoundOlder
		}
		_, err = d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &d.table, Key: map[string]types.AttributeValue{"PK": it["PK"], "SK": it["SK"]}})
		return err
	})
	if errors.Is(err, errFoundOlder) {
		return true, nil
	}
	return false, wrapD("has older event", err)
}
