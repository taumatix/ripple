package store

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// The event sequence is one counter item, PK=EVENTSEQ SK=META N=<last number>. An event is
// numbered by a transaction that moves the counter from the value read to the next one and
// puts the event, each conditional on what was read, so two replicas never take the same
// number and a number is visible only together with its event. A lost race rereads and
// tries again; events arrive at webhook rates, far from contention that would matter.
const eventSeqPK = "EVENTSEQ"

type eventSeqAttrs struct {
	item
	N int64
}

// maxSeqAttempts bounds the retries of a contended counter before the error surfaces (the
// receiver then answers 5xx and the host redelivers).
const maxSeqAttempts = 25

// EnqueueSequenced is store.EventSequencer.
func (d *Dynamo) EnqueueSequenced(ctx context.Context, e model.Event) (model.Event, bool, error) {
	for range maxSeqAttempts {
		var have eventAttrs
		if found, err := d.get(ctx, "EVENT#"+e.ID, "META", &have); err != nil {
			return e, false, wrapD("enqueue sequenced event", err)
		} else if found {
			return e, false, nil
		}
		var c eventSeqAttrs
		found, err := d.get(ctx, eventSeqPK, "META", &c)
		if err != nil {
			return e, false, wrapD("enqueue sequenced event", err)
		}
		next := firstEventSeq()
		if found {
			next = max(c.N+1, next)
		}
		e.Seq = next
		ev, err := attributevalue.MarshalMap(eventItem(e))
		if err != nil {
			return e, false, wrapD("enqueue sequenced event", err)
		}
		ctr, err := attributevalue.MarshalMap(eventSeqAttrs{item: item{PK: eventSeqPK, SK: "META"}, N: next})
		if err != nil {
			return e, false, wrapD("enqueue sequenced event", err)
		}
		put := &types.Put{TableName: &d.table, Item: ctr, ConditionExpression: aws.String("attribute_not_exists(PK)")}
		if found {
			put.ConditionExpression = aws.String("#n = :n")
			put.ExpressionAttributeNames = map[string]string{"#n": "N"}
			put.ExpressionAttributeValues = map[string]types.AttributeValue{":n": n(c.N)}
		}
		_, err = d.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: &d.table, Item: ev, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
			{Put: put},
		}})
		var tc *types.TransactionCanceledException
		if errors.As(err, &tc) {
			continue // the counter moved or the event appeared: look again
		}
		if err != nil {
			return e, false, wrapD("enqueue sequenced event", err)
		}
		return e, true, nil
	}
	return e, false, wrapD("enqueue sequenced event", errors.New("event sequence is contended"))
}
