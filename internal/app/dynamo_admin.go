package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/taumatix/ripple/internal/store"
)

var errNotDynamo = errors.New("this command needs RIPPLE_STORE=dynamodb")

func dynamoOf(st store.Store) (*store.Dynamo, error) {
	d, ok := st.(*store.Dynamo)
	if !ok {
		return nil, errNotDynamo
	}
	return d, nil
}

// DynamoCheckCounts prints every package whose dependents counter disagrees with its
// index. It writes nothing to the table and scans all of it.
func DynamoCheckCounts(ctx context.Context, st store.Store, out io.Writer) error {
	d, err := dynamoOf(st)
	if err != nil {
		return err
	}
	drift, err := d.CheckDependentCounts(ctx)
	if err != nil {
		return err
	}
	if len(drift) == 0 {
		fmt.Fprintln(out, "counters match the index")
		return nil
	}
	for _, x := range drift {
		fmt.Fprintf(out, "%s  indexed=%d counted=%d\n", x.Base, x.Indexed, x.Counted)
	}
	fmt.Fprintf(out, "%d package(s) drifted; run the recount to repair\n", len(drift))
	return nil
}

// DynamoRecount recounts every package's dependents (RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS
// must be set, as the recount may grow a package's shards).
func DynamoRecount(ctx context.Context, st store.Store, out io.Writer) error {
	d, err := dynamoOf(st)
	if err != nil {
		return err
	}
	n, err := d.RecountAllDependents(ctx)
	if n > 0 || err == nil {
		fmt.Fprintf(out, "recounted %d package(s)\n", n)
	}
	return err
}

// DynamoBackfillFailedJobs writes the missing open-job copies of failed jobs.
func DynamoBackfillFailedJobs(ctx context.Context, st store.Store, out io.Writer) error {
	d, err := dynamoOf(st)
	if err != nil {
		return err
	}
	n, err := d.BackfillFailedJobs(ctx)
	if err == nil {
		fmt.Fprintf(out, "wrote %d open-job copies of failed jobs\n", n)
	}
	return err
}
