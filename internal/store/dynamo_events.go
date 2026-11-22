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

// Events are stored as
//
//	event  PK=EVENT#<id>  SK=META  GSI1PK=EVENTS  GSI1SK=<seq, 20 digits>\x1f<id>
//
// so ListEvents is one Query on gsi1 in Seq order. One GSI partition holds the whole
// queue; the backlog is capped by the receiver, which keeps it far below the
// partition's throughput limits.
type eventAttrs struct {
	item
	ID, Kind, Repo, Ref string
	Seq                 int64
	InstallationID      int64
	Files               []string `dynamodbav:",omitempty"`
	Attempts            int
	NotBefore           string
}

func eventItem(e model.Event) eventAttrs {
	return eventAttrs{
		item: item{PK: "EVENT#" + e.ID, SK: "META", GSI1PK: "EVENTS", GSI1SK: fmt.Sprintf("%020d", e.Seq) + sep + e.ID},
		ID:   e.ID, Kind: e.Kind, Repo: e.Repo, Ref: e.Ref, Seq: e.Seq, InstallationID: e.InstallationID,
		Files: e.Files, Attempts: e.Attempts, NotBefore: ts(e.NotBefore),
	}
}

func (d *Dynamo) EnqueueEvent(ctx context.Context, e model.Event) (bool, error) {
	err := d.put(ctx, eventItem(e), aws.String("attribute_not_exists(PK)"))
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return false, nil
	}
	return err == nil, wrapD("enqueue event", err)
}

// ListEvents reads gsi1, which is eventually consistent: an event enqueued a moment
// ago may be missing. The receiver hands new events to the workers directly; only a
// restart reads this, long after the writes.
func (d *Dynamo) ListEvents(ctx context.Context) ([]model.Event, error) {
	var out []model.Event
	err := d.query(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(gsi1),
		KeyConditionExpression:    aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("EVENTS")},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a eventAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		out = append(out, model.Event{ID: a.ID, Seq: a.Seq, InstallationID: a.InstallationID, Kind: a.Kind, Repo: a.Repo,
			Ref: a.Ref, Files: a.Files, Attempts: a.Attempts, NotBefore: parseTS(a.NotBefore)})
		return nil
	})
	return out, wrapD("list events", err)
}

func (d *Dynamo) UpdateEvent(ctx context.Context, e model.Event) error {
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 &d.table,
		Key:                       map[string]types.AttributeValue{"PK": s("EVENT#" + e.ID), "SK": s("META")},
		UpdateExpression:          aws.String("SET Attempts = :a, NotBefore = :nb"),
		ConditionExpression:       aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":a": &types.AttributeValueMemberN{Value: fmt.Sprint(e.Attempts)}, ":nb": s(ts(e.NotBefore))},
	})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return nil
	}
	return wrapD("update event", err)
}

func (d *Dynamo) DeleteEvent(ctx context.Context, id string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key:       map[string]types.AttributeValue{"PK": s("EVENT#" + id), "SK": s("META")},
	})
	return wrapD("delete event", err)
}
