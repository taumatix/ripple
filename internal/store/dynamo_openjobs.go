package store

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/taumatix/ripple/internal/model"
)

// The per-repo open-job copies (PK=REPO#<repo>, SK=OPENJOB#<key>) are written after the job's
// own item, so two things can be wrong with them: a job written by a ripple older than the
// copies has none, and a crash between the two writes leaves one stale. Both are repaired
// here, without a Scan:
//
//   - backfillOpenJobs runs from EnsureTable. Every pending job is in the sparse pending index,
//     so it reads each shard of that queue (the queue is the work still to do, normally small),
//     checks which copies exist with BatchGetItem and writes only the missing ones, each with
//     attribute_not_exists, so it never replaces a copy a live write put there. It runs on every
//     start rather than once: a job enqueued by a not-yet-upgraded replica during a rolling
//     deploy has no copy either, and the next start heals it.
//   - ListOpenJobs reconciles each copy with its job (BatchGetItem, consistent): a job that is
//     no longer open, or no longer exists, loses its copy and is not listed; a job that changed
//     is listed as the job is, and its copy is rewritten.
//
// A failed job from before the copies is in neither the index nor a copy, and finding it would
// take a Scan of the table; it stays unlisted until its next write (ROADMAP).

// batchGetMax is DynamoDB's BatchGetItem limit.
const batchGetMax = 100

// batchGetJobs reads the job items for keys with strongly consistent BatchGetItems of at
// most 100, retrying UnprocessedKeys. The result is by job key; an absent job is not in it.
func (d *Dynamo) batchGetJobs(ctx context.Context, keys []string) (map[string]jobAttrs, error) {
	out := make(map[string]jobAttrs, len(keys))
	for len(keys) > 0 {
		chunk := keys[:min(batchGetMax, len(keys))]
		keys = keys[len(chunk):]
		var pending []map[string]types.AttributeValue
		for _, k := range chunk {
			pending = append(pending, map[string]types.AttributeValue{"PK": s("JOB#" + k), "SK": s("META")})
		}
		for attempt := 0; len(pending) > 0; attempt++ {
			r, err := d.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: map[string]types.KeysAndAttributes{
				d.table: {Keys: pending, ConsistentRead: aws.Bool(true)}}})
			if err != nil {
				return nil, err
			}
			for _, it := range r.Responses[d.table] {
				var a jobAttrs
				if err := attributevalue.UnmarshalMap(it, &a); err != nil {
					return nil, err
				}
				out[a.Key] = a
			}
			pending = r.UnprocessedKeys[d.table].Keys
			if len(pending) > 0 {
				if attempt > 8 {
					return nil, errors.New("batch get: unprocessed keys remain after retries")
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
				}
			}
		}
	}
	return out, nil
}

// openCopyKey is the table key of a job's repo-partition copy.
func openCopyKey(repo, key string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s("REPO#" + repo), "SK": s("OPENJOB#" + key)}
}

// ListOpenJobs is store.OpenJobLister: one Query on the repo's partition, then each copy is
// reconciled with its job (see above), so a stale copy cannot show a finished job as open.
func (d *Dynamo) ListOpenJobs(ctx context.Context, repo string) ([]model.Job, error) {
	var copies []jobAttrs
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk AND begins_with(SK, :p)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s("REPO#" + repo), ":p": s("OPENJOB#")},
	}, 0, func(it map[string]types.AttributeValue) error {
		var a jobAttrs
		if err := attributevalue.UnmarshalMap(it, &a); err != nil {
			return err
		}
		copies = append(copies, a)
		return nil
	})
	if err != nil {
		return nil, wrapD("list open jobs", err)
	}
	keys := make([]string, 0, len(copies))
	for _, c := range copies {
		keys = append(keys, c.Key)
	}
	jobs, err := d.batchGetJobs(ctx, keys)
	if err != nil {
		return nil, wrapD("list open jobs", err)
	}
	var out []model.Job
	for _, c := range copies {
		a, found := jobs[c.Key]
		if !found || !isOpen(fromJobItem(a)) {
			// The job finished (or never existed) and its copy was not removed. Best effort:
			// a failed delete leaves it for the next read, and it is not listed either way.
			_, _ = d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &d.table, Key: openCopyKey(repo, c.Key)})
			continue
		}
		j := fromJobItem(a)
		if fromJobItem(c) != j {
			_ = d.syncOpenJob(ctx, j) // repair; the next write or read retries it
		}
		out = append(out, j)
	}
	return out, nil
}

// backfillOpenJobs writes the missing open-job copies of every pending job (see above).
func (d *Dynamo) backfillOpenJobs(ctx context.Context) error {
	parts, err := fanOut(ctx, d.jobShards, d.concurrency, func(ctx context.Context, sh int) ([]jobAttrs, error) {
		var part []jobAttrs
		err := d.query(ctx, &dynamodb.QueryInput{
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pendingJobsPK(sh))},
		}, 0, func(it map[string]types.AttributeValue) error {
			var a jobAttrs
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return err
			}
			part = append(part, a)
			return nil
		})
		return part, err
	})
	if err != nil {
		return wrapD("backfill open jobs", err)
	}
	for _, part := range parts {
		if _, err := d.backfillCopies(ctx, part); err != nil {
			return wrapD("backfill open jobs", err)
		}
	}
	return nil
}

// backfillCopies puts the copies of jobs that lack one, 100 existence checks at a time,
// and returns how many it wrote.
func (d *Dynamo) backfillCopies(ctx context.Context, jobs []jobAttrs) (int, error) {
	wrote := 0
	for len(jobs) > 0 {
		chunk := jobs[:min(batchGetMax, len(jobs))]
		jobs = jobs[len(chunk):]
		have, err := d.existingCopies(ctx, chunk)
		if err != nil {
			return wrote, err
		}
		for _, a := range chunk {
			if have[a.Key] {
				continue
			}
			c := a
			c.item = item{PK: "REPO#" + a.Repo, SK: "OPENJOB#" + a.Key}
			cond := "attribute_not_exists(PK)"
			if err := d.put(ctx, c, &cond); err != nil {
				var cc *types.ConditionalCheckFailedException
				if errors.As(err, &cc) {
					continue // a live write got there first
				}
				return wrote, err
			}
			wrote++
		}
	}
	return wrote, nil
}

// existingCopies reports which of the jobs already have an open-job copy.
func (d *Dynamo) existingCopies(ctx context.Context, jobs []jobAttrs) (map[string]bool, error) {
	have := map[string]bool{}
	var pending []map[string]types.AttributeValue
	byKey := map[string]string{} // SK -> job key (PK is per repo, so key by both)
	for _, a := range jobs {
		pending = append(pending, openCopyKey(a.Repo, a.Key))
		byKey["REPO#"+a.Repo+"\x00OPENJOB#"+a.Key] = a.Key
	}
	for attempt := 0; len(pending) > 0; attempt++ {
		r, err := d.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: map[string]types.KeysAndAttributes{
			d.table: {Keys: pending, ConsistentRead: aws.Bool(true), ProjectionExpression: aws.String("PK, SK")}}})
		if err != nil {
			return nil, err
		}
		for _, it := range r.Responses[d.table] {
			pk, sk := it["PK"].(*types.AttributeValueMemberS).Value, it["SK"].(*types.AttributeValueMemberS).Value
			have[byKey[pk+"\x00"+sk]] = true
		}
		pending = r.UnprocessedKeys[d.table].Keys
		if len(pending) > 0 {
			if attempt > 8 {
				return nil, errors.New("batch get: unprocessed keys remain after retries")
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
			}
		}
	}
	return have, nil
}
