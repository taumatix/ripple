package store

import (
	"context"
	"time"
)

// Lease is a named, expiring claim held by one owner. Token is the fencing token: it
// grows each time the name is granted to a new holder (or regranted after it lapsed),
// so a holder that was slow to notice it lost the lease is told so by RenewLease
// instead of being able to act as the current holder.
//
// Expiry is judged by the store: Postgres uses its own now(), so replicas' clocks do not
// matter; Memory is one process; DynamoDB has no clock and uses the replicas' wall
// clocks with a skew margin (see Dynamo).
type Lease struct {
	Name, Owner string
	Token       int64
	Expires     time.Time
}

// Leases lets replicas that share one store take turns at a piece of work.
type Leases interface {
	// AcquireLease claims name for owner for ttl. It succeeds when nobody holds the
	// name, the holder's lease has lapsed, or owner already holds it (which extends
	// it and keeps the token). The bool is false when another owner holds it.
	AcquireLease(ctx context.Context, name, owner string, ttl time.Duration) (Lease, bool, error)
	// RenewLease extends l by ttl from now. It reports false, and changes nothing,
	// when l is no longer the current grant: another owner took the name over, or it
	// was released. A lapsed lease nobody took over may still be renewed.
	RenewLease(ctx context.Context, l Lease, ttl time.Duration) (Lease, bool, error)
	// ReleaseLease gives l up so the name can be claimed at once. Releasing a lease
	// that is no longer current is not an error.
	ReleaseLease(ctx context.Context, l Lease) error
}
