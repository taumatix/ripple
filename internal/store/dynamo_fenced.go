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

// leaseCheck is the transaction item that fails the whole write unless l is the live grant.
func (d *Dynamo) leaseCheck(l Lease) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{TableName: &d.table, Key: leaseKey(l.Name),
		ConditionExpression:       aws.String("#o = :o AND #t = :t"),
		ExpressionAttributeNames:  map[string]string{"#o": "Owner", "#t": "Token"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":o": s(l.Owner), ":t": &types.AttributeValueMemberN{Value: fmt.Sprint(l.Token)}}}}
}

// fencedWrite runs w behind the lease check. A cancelled transaction names its failed item:
// the lease (item 0) is ErrLeaseLost; any other failure is the write's own condition, which
// each caller declares benign through gone.
func (d *Dynamo) fencedWrite(ctx context.Context, op string, l Lease, gone bool, ws ...types.TransactWriteItem) error {
	_, err := d.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: append([]types.TransactWriteItem{d.leaseCheck(l)}, ws...)})
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) {
		if gone && len(tc.CancellationReasons) == 1+len(ws) && aws.ToString(tc.CancellationReasons[0].Code) == "None" {
			return nil
		}
		return ErrLeaseLost
	}
	return wrapD(op, err)
}

// SetCursorFenced is store.Store.
func (d *Dynamo) SetCursorFenced(ctx context.Context, n, v string, l Lease) error {
	m, err := attributevalue.MarshalMap(struct {
		item
		Value string
	}{item{PK: "CURSOR#" + n, SK: "META"}, v})
	if err != nil {
		return wrapD("set cursor", err)
	}
	return d.fencedWrite(ctx, "set cursor", l, false, types.TransactWriteItem{Put: &types.Put{TableName: &d.table, Item: m}})
}

// UpdateEventFenced is store.Store. Transactions here carry a Put rather than an Update, so
// the whole item is rewritten, guarded by attribute_exists so a deleted event stays deleted;
// the event's marker follows it (see dynamo_eventmarks.go).
func (d *Dynamo) UpdateEventFenced(ctx context.Context, e model.Event, l Lease) error {
	ws, err := d.eventWrites(e, aws.String("attribute_exists(PK)"), nil)
	if err != nil {
		return wrapD("update event", err)
	}
	return d.fencedWrite(ctx, "update event", l, true, ws...)
}

// DeleteEventFenced is store.Store.
func (d *Dynamo) DeleteEventFenced(ctx context.Context, id string, l Lease) error {
	ws, err := d.deleteEventWrites(ctx, id)
	if err != nil {
		return wrapD("delete event", err)
	}
	if ws == nil {
		ws = []types.TransactWriteItem{{Delete: &types.Delete{TableName: &d.table,
			Key: map[string]types.AttributeValue{"PK": s("EVENT#" + id), "SK": s("META")}}}}
	}
	return d.fencedWrite(ctx, "delete event", l, false, ws...)
}
