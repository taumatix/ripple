package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// Events are stored as
//
//	event  PK=EVENT#<id>  SK=META  GSI1PK=EVENTS  GSI1SK=<seq, 20 digits>\x1f<id>
//	dead   PK=EVENT#<id>  SK=META  GSI1PK=EVENTS#dead  GSI1SK=<dead_at>\x1f<id>
//
// so ListEvents is one Query on gsi1 in Seq order. A dead-lettered event moves to the
// dead partition (ListDeadEvents, in DeadAt order; PurgeDeadEvents reads a key range of it)
// and leaves the live one; items written before dead letters existed have none of the new
// attributes and read as live. One GSI partition holds the whole
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
	LastError           string `dynamodbav:",omitempty"`
	LastAttemptAt       string `dynamodbav:",omitempty"`
	DeadAt              string `dynamodbav:",omitempty"`
	NewRepo             string `dynamodbav:",omitempty"`
	DefaultBranch       string `dynamodbav:",omitempty"`
	RepoID              int64  `dynamodbav:",omitempty"`
}

const deadEventsPK = "EVENTS#dead"

// eventKeys are the index keys of e: the live queue in Seq order, or the dead partition.
func eventKeys(e model.Event) (pk, sk string) {
	if e.Dead() {
		return deadEventsPK, ts(e.DeadAt) + sep + e.ID
	}
	return "EVENTS", fmt.Sprintf("%020d", e.Seq) + sep + e.ID
}

// tsOrEmpty stores the zero time as an absent attribute.
func tsOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return ts(t)
}

func fromEventAttrs(a eventAttrs) model.Event {
	return model.Event{ID: a.ID, Seq: a.Seq, InstallationID: a.InstallationID, Kind: a.Kind, Repo: a.Repo,
		Ref: a.Ref, Files: a.Files, Attempts: a.Attempts, NotBefore: parseTS(a.NotBefore),
		LastError: a.LastError, LastAttemptAt: parseTS(a.LastAttemptAt), DeadAt: parseTS(a.DeadAt),
		NewRepo: a.NewRepo, DefaultBranch: a.DefaultBranch, RepoID: a.RepoID}
}

func eventItem(e model.Event) eventAttrs {
	pk, sk := eventKeys(e)
	return eventAttrs{
		item: item{PK: "EVENT#" + e.ID, SK: "META", GSI1PK: pk, GSI1SK: sk},
		ID:   e.ID, Kind: e.Kind, Repo: e.Repo, Ref: e.Ref, Seq: e.Seq, InstallationID: e.InstallationID,
		Files: e.Files, Attempts: e.Attempts, NotBefore: ts(e.NotBefore),
		LastError: e.LastError, LastAttemptAt: tsOrEmpty(e.LastAttemptAt), DeadAt: tsOrEmpty(e.DeadAt),
		NewRepo: e.NewRepo, DefaultBranch: e.DefaultBranch, RepoID: e.RepoID,
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
	return d.listEventPartition(ctx, "list events", "EVENTS")
}

// ListDeadEvents reads the dead partition, eventually consistent like ListEvents.
func (d *Dynamo) ListDeadEvents(ctx context.Context) ([]model.Event, error) {
	return d.listEventPartition(ctx, "list dead events", deadEventsPK)
}

func (d *Dynamo) listEventPartition(ctx context.Context, op, pk string) ([]model.Event, error) {
	var out []model.Event
	err := d.query(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(gsi1),
		KeyConditionExpression:    aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pk)},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a eventAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		out = append(out, fromEventAttrs(a))
		return nil
	})
	return out, wrapD(op, err)
}

func (d *Dynamo) UpdateEvent(ctx context.Context, e model.Event) error {
	pk, sk := eventKeys(e)
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           &d.table,
		Key:                 map[string]types.AttributeValue{"PK": s("EVENT#" + e.ID), "SK": s("META")},
		UpdateExpression:    aws.String("SET #a = :a, #nb = :nb, #le = :le, #la = :la, #da = :da, GSI1PK = :gpk, GSI1SK = :gsk"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
		ExpressionAttributeNames: map[string]string{"#a": "Attempts", "#nb": "NotBefore", "#le": "LastError",
			"#la": "LastAttemptAt", "#da": "DeadAt"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":a": &types.AttributeValueMemberN{Value: fmt.Sprint(e.Attempts)},
			":nb": s(ts(e.NotBefore)), ":le": s(e.LastError), ":la": s(tsOrEmpty(e.LastAttemptAt)), ":da": s(tsOrEmpty(e.DeadAt)),
			":gpk": s(pk), ":gsk": s(sk)},
	})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return nil
	}
	return wrapD("update event", err)
}

// RequeueDeadEvent reads the item (strongly consistent) and rewrites its keys back to the
// live queue, conditional on it still being dead.
func (d *Dynamo) RequeueDeadEvent(ctx context.Context, id string) (bool, error) {
	var a eventAttrs
	ok, err := d.get(ctx, "EVENT#"+id, "META", &a)
	if err != nil || !ok || a.DeadAt == "" {
		return false, wrapD("requeue dead event", err)
	}
	e := fromEventAttrs(a)
	e.Attempts, e.NotBefore, e.DeadAt = 0, time.Time{}, time.Time{}
	err = d.putIfDead(ctx, e)
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return false, nil
	}
	return err == nil, wrapD("requeue dead event", err)
}

func (d *Dynamo) putIfDead(ctx context.Context, e model.Event) error {
	it, err := attributevalue.MarshalMap(eventItem(e))
	if err != nil {
		return err
	}
	_, err = d.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.table, Item: it,
		ConditionExpression:       aws.String("GSI1PK = :dead"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":dead": s(deadEventsPK)}})
	return err
}

// PurgeDeadEvents queries the dead partition's key range before the cut-off and deletes
// what it finds. The index is eventually consistent, so an event that died a moment ago
// may wait for the next purge; one just purged may be listed once more.
func (d *Dynamo) PurgeDeadEvents(ctx context.Context, before time.Time) (int, error) {
	var ids []string
	err := d.query(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(gsi1),
		KeyConditionExpression:    aws.String("GSI1PK = :pk AND GSI1SK < :max"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(deadEventsPK), ":max": s(ts(before))},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a eventAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		ids = append(ids, a.ID)
		return nil
	})
	if err != nil {
		return 0, wrapD("purge dead events", err)
	}
	n := 0
	for _, id := range ids {
		// Conditional: an event requeued since the query is live again and stays.
		_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:                 &d.table,
			Key:                       map[string]types.AttributeValue{"PK": s("EVENT#" + id), "SK": s("META")},
			ConditionExpression:       aws.String("GSI1PK = :dead"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":dead": s(deadEventsPK)},
		})
		var cc *types.ConditionalCheckFailedException
		switch {
		case errors.As(err, &cc):
		case err != nil:
			return n, wrapD("purge dead events", err)
		default:
			n++
		}
	}
	return n, nil
}

func (d *Dynamo) DeleteEvent(ctx context.Context, id string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key:       map[string]types.AttributeValue{"PK": s("EVENT#" + id), "SK": s("META")},
	})
	return wrapD("delete event", err)
}
