package store

import (
	"context"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// CursorGroupSep separates a cursor's group from its key in "<group>#<key>". DynamoDB
// indexes such a cursor under its group so ListCursors reads one index partition.
const CursorGroupSep = "#"

func sortCursors(c []Cursor) { sort.Slice(c, func(i, j int) bool { return c[i].Name < c[j].Name }) }

// ListCursors is store.CursorLister.
func (m *Memory) ListCursors(_ context.Context, prefix string) ([]Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cursor
	for k, v := range m.cursors {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Cursor{k, v})
		}
	}
	sortCursors(out)
	return out, nil
}

// ListCursorsPage is store.PagedCursorLister.
func (m *Memory) ListCursorsPage(ctx context.Context, prefix, after string, limit int) ([]Cursor, error) {
	all, err := m.ListCursors(ctx, prefix)
	return pageCursors(all, after, limit), err
}

// pageCursors slices a name-sorted listing to the limit cursors after the name after.
func pageCursors(sorted []Cursor, after string, limit int) []Cursor {
	if limit < 1 {
		return nil
	}
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i].Name > after })
	sorted = sorted[i:]
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

// DeleteCursor is store.CursorLister.
func (m *Memory) DeleteCursor(_ context.Context, n string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cursors, n)
	return nil
}

// ListCursors is store.CursorLister. The prefix is matched with starts_with, not LIKE.
func (p *Postgres) ListCursors(ctx context.Context, prefix string) ([]Cursor, error) {
	rows, err := p.pool.Query(ctx, `SELECT name, value FROM cursors WHERE starts_with(name, $1) ORDER BY name`, prefix)
	if err != nil {
		return nil, wrap("list cursors", err)
	}
	defer rows.Close()
	var out []Cursor
	for rows.Next() {
		var c Cursor
		if err := rows.Scan(&c.Name, &c.Value); err != nil {
			return nil, wrap("list cursors", err)
		}
		out = append(out, c)
	}
	return out, wrap("list cursors", rows.Err())
}

// ListCursorsPage is store.PagedCursorLister. Names compare bytewise (COLLATE "C"), as in
// Memory, whatever the database's collation.
func (p *Postgres) ListCursorsPage(ctx context.Context, prefix, after string, limit int) ([]Cursor, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT name, value FROM cursors
		WHERE starts_with(name, $1) AND name COLLATE "C" > $2 COLLATE "C"
		ORDER BY name COLLATE "C" LIMIT $3`, prefix, after, limit)
	if err != nil {
		return nil, wrap("list cursors page", err)
	}
	defer rows.Close()
	var out []Cursor
	for rows.Next() {
		var c Cursor
		if err := rows.Scan(&c.Name, &c.Value); err != nil {
			return nil, wrap("list cursors page", err)
		}
		out = append(out, c)
	}
	return out, wrap("list cursors page", rows.Err())
}

// DeleteCursor is store.CursorLister.
func (p *Postgres) DeleteCursor(ctx context.Context, n string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM cursors WHERE name=$1`, n)
	return wrap("delete cursor", err)
}

// cursorGroupPK is the gsi1 partition of a cursor group.
func cursorGroupPK(group string) string { return "CURSORS#" + group }

// cursorItem is the stored form of one cursor. A name with a group is also written to gsi1
// (GSI1PK=CURSORS#<group>, GSI1SK=<name>) so a group lists without a scan.
func cursorItem(n, v string) any {
	it := item{PK: "CURSOR#" + n, SK: "META"}
	if g, _, ok := strings.Cut(n, CursorGroupSep); ok {
		it.GSI1PK, it.GSI1SK = cursorGroupPK(g), n
	}
	return struct {
		item
		Value string
	}{it, v}
}

// ListCursors is store.CursorLister. A prefix containing the group separator is a Query of
// that group's index partition (eventually consistent); any other prefix is a filtered Scan
// of the table, as PurgeCursors is.
func (d *Dynamo) ListCursors(ctx context.Context, prefix string) ([]Cursor, error) {
	var out []Cursor
	collect := func(items []map[string]types.AttributeValue) error {
		for _, it := range items {
			var a struct {
				PK    string
				Value string
			}
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return err
			}
			out = append(out, Cursor{strings.TrimPrefix(a.PK, "CURSOR#"), a.Value})
		}
		return nil
	}
	var start map[string]types.AttributeValue
	for {
		var items []map[string]types.AttributeValue
		var next map[string]types.AttributeValue
		if group, _, ok := strings.Cut(prefix, CursorGroupSep); ok {
			r, err := d.db.Query(ctx, &dynamodb.QueryInput{
				TableName:                 &d.table,
				IndexName:                 aws.String(gsi1),
				KeyConditionExpression:    aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :p)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(cursorGroupPK(group)), ":p": s(prefix)},
				ExclusiveStartKey:         start,
			})
			if err != nil {
				return nil, wrapD("list cursors", err)
			}
			items, next = r.Items, r.LastEvaluatedKey
		} else {
			r, err := d.db.Scan(ctx, &dynamodb.ScanInput{
				TableName:                 &d.table,
				FilterExpression:          aws.String("begins_with(PK, :p)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":p": s("CURSOR#" + prefix)},
				ExclusiveStartKey:         start,
			})
			if err != nil {
				return nil, wrapD("list cursors", err)
			}
			items, next = r.Items, r.LastEvaluatedKey
		}
		if err := collect(items); err != nil {
			return nil, wrapD("list cursors", err)
		}
		if start = next; len(start) == 0 {
			break
		}
	}
	sortCursors(out)
	return out, nil
}

// DeleteCursor is store.CursorLister.
func (d *Dynamo) DeleteCursor(ctx context.Context, n string) error {
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table,
		Key:       map[string]types.AttributeValue{"PK": s("CURSOR#" + n), "SK": s("META")},
	})
	return wrapD("delete cursor", err)
}

// ListCursorsPage is store.PagedCursorLister. A prefix with a group is a Query of that group's
// index partition that starts after the given name and stops at the limit, so a page reads one
// page; any other prefix is listed whole by ListCursors and sliced.
func (d *Dynamo) ListCursorsPage(ctx context.Context, prefix, after string, limit int) ([]Cursor, error) {
	if limit < 1 {
		return nil, nil
	}
	group, _, ok := strings.Cut(prefix, CursorGroupSep)
	if !ok {
		all, err := d.ListCursors(ctx, prefix)
		return pageCursors(all, after, limit), err
	}
	var start map[string]types.AttributeValue
	switch {
	case after == "" || after < prefix:
		// from the beginning of the prefix; a start key below the range is refused by DynamoDB
	case !strings.HasPrefix(after, prefix):
		return nil, nil
	default:
		start = map[string]types.AttributeValue{
			"PK": s("CURSOR#" + after), "SK": s("META"),
			"GSI1PK": s(cursorGroupPK(group)), "GSI1SK": s(after),
		}
	}
	var out []Cursor
	for len(out) < limit {
		r, err := d.db.Query(ctx, &dynamodb.QueryInput{
			TableName:                 &d.table,
			IndexName:                 aws.String(gsi1),
			KeyConditionExpression:    aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :p)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(cursorGroupPK(group)), ":p": s(prefix)},
			ExclusiveStartKey:         start,
			Limit:                     aws.Int32(int32(limit - len(out))),
		})
		if err != nil {
			return nil, wrapD("list cursors page", err)
		}
		for _, it := range r.Items {
			var a struct {
				PK    string
				Value string
			}
			if err := attributevalue.UnmarshalMap(it, &a); err != nil {
				return nil, wrapD("list cursors page", err)
			}
			out = append(out, Cursor{strings.TrimPrefix(a.PK, "CURSOR#"), a.Value})
		}
		if start = r.LastEvaluatedKey; len(start) == 0 {
			break
		}
	}
	return out, nil
}
