package store

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// DefaultLeaseSkew is how long past its stored expiry a lease must be before another
// replica may take it over: the clock difference between replicas the lease tolerates.
const DefaultLeaseSkew = 5 * time.Second

// WithLeaseSkew sets the margin; replicas' wall clocks must agree to within it.
func WithLeaseSkew(d time.Duration) DynamoOption { return func(x *Dynamo) { x.leaseSkew = d } }

// Leases are PK=LEASE#<name> SK=META with Owner, Token and Expires. DynamoDB has no
// server clock to compare against, so Expires is the grantor's wall clock plus ttl and a
// contender must see now > Expires + skew. Every change is conditional on the item being
// as it was read (owner, token, expiry), so two racers cannot both win.
type leaseAttrs struct {
	item
	Owner, Expires string
	Token          int64
}

func leaseKey(name string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s("LEASE#" + name), "SK": s("META")}
}

func (d *Dynamo) AcquireLease(ctx context.Context, name, owner string, ttl time.Duration) (Lease, bool, error) {
	var cur leaseAttrs
	found, err := d.get(ctx, "LEASE#"+name, "META", &cur)
	if err != nil {
		return Lease{}, false, wrapD("acquire lease", err)
	}
	now := time.Now()
	want := leaseAttrs{item: item{PK: "LEASE#" + name, SK: "META"}, Owner: owner, Token: 1, Expires: ts(now.Add(ttl))}
	var cond string
	var vals map[string]types.AttributeValue
	switch {
	case !found:
		cond = "attribute_not_exists(PK)"
	default:
		expires := parseTS(cur.Expires)
		mine := cur.Owner == owner
		lapsed := now.After(expires.Add(d.leaseSkew))
		switch {
		case mine && now.Before(expires):
			want.Token = cur.Token
		case mine || lapsed:
			want.Token = cur.Token + 1
		default:
			return Lease{}, false, nil
		}
		cond = "#o = :o AND #t = :t AND #e = :e"
		vals = map[string]types.AttributeValue{":o": s(cur.Owner), ":t": n(cur.Token), ":e": s(cur.Expires)}
	}
	err = d.putCond(ctx, want, cond, vals)
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, wrapD("acquire lease", err)
	}
	return Lease{Name: name, Owner: owner, Token: want.Token, Expires: parseTS(want.Expires)}, true, nil
}

func (d *Dynamo) RenewLease(ctx context.Context, l Lease, ttl time.Duration) (Lease, bool, error) {
	exp := time.Now().Add(ttl)
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table, Key: leaseKey(l.Name),
		UpdateExpression:          aws.String("SET #e = :e"),
		ConditionExpression:       aws.String("#o = :o AND #t = :t"),
		ExpressionAttributeNames:  map[string]string{"#e": "Expires", "#o": "Owner", "#t": "Token"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":e": s(ts(exp)), ":o": s(l.Owner), ":t": n(l.Token)},
	})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, wrapD("renew lease", err)
	}
	l.Expires = exp
	return l, true, nil
}

func (d *Dynamo) ReleaseLease(ctx context.Context, l Lease) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table, Key: leaseKey(l.Name),
		ConditionExpression:       aws.String("#o = :o AND #t = :t"),
		ExpressionAttributeNames:  map[string]string{"#o": "Owner", "#t": "Token"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":o": s(l.Owner), ":t": n(l.Token)},
	})
	var cc *types.ConditionalCheckFailedException
	if errors.As(err, &cc) {
		return nil
	}
	return wrapD("release lease", err)
}

func (d *Dynamo) GetEvent(ctx context.Context, id string) (model.Event, bool, error) {
	var a eventAttrs
	ok, err := d.get(ctx, "EVENT#"+id, "META", &a)
	if err != nil || !ok {
		return model.Event{}, false, wrapD("get event", err)
	}
	return fromEventAttrs(a), true, nil
}
