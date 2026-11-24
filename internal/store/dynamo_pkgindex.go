package store

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// The package index: one item per package that has had a dependent (PK=PKGS, SK=<base>),
// written by the first ReplaceDependencies of a process that adds a dependent of that
// package. The hygiene commands (CheckDependentCounts, RecountAllDependents) list packages
// from it with one Query instead of Scanning every item of the table for DEP# keys.
//
// The index is a superset: nothing removes an entry when the last dependent goes (the
// commands see a package with none as consistent, and RecountAllDependents prunes it). A
// table written before the index existed is indexed once, by one Scan, which then writes a
// marker (PK=PKGSMETA, SK=BUILT); the marker is only written after a complete pass, so a
// cancelled build runs again. A writer of an older version adds no entries, so a package it
// alone introduces after the marker is missing until the index is rebuilt (delete the
// marker item to force one).
const (
	pkgIndexPK  = "PKGS"
	pkgMarkerPK = "PKGSMETA"
)

// indexPackages records bases in the index, once per process each.
func (d *Dynamo) indexPackages(ctx context.Context, bases []string) error {
	for _, b := range bases {
		if b == "" {
			continue
		}
		if _, done := d.pkgIndexed.Load(b); done {
			continue
		}
		if err := d.put(ctx, item{PK: pkgIndexPK, SK: b}, nil); err != nil {
			return err
		}
		d.pkgIndexed.Store(b, true)
	}
	return nil
}

// dependentBases lists every package that has had a dependent, from the index.
func (d *Dynamo) dependentBases(ctx context.Context) ([]string, error) {
	if err := d.ensurePackageIndex(ctx); err != nil {
		return nil, err
	}
	var out []string
	err := d.query(ctx, &dynamodb.QueryInput{
		ConsistentRead:            aws.Bool(true),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ProjectionExpression:      aws.String("SK"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pkgIndexPK)},
	}, 0, func(it map[string]types.AttributeValue) error {
		out = append(out, it["SK"].(*types.AttributeValueMemberS).Value)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ensurePackageIndex indexes the packages of dependents stored before the index existed, once.
func (d *Dynamo) ensurePackageIndex(ctx context.Context) error {
	var m item
	if ok, err := d.get(ctx, pkgMarkerPK, "BUILT", &m); err != nil || ok {
		return err
	}
	seen := map[string]bool{}
	err := d.scanItems(ctx, "begins_with(SK, :dep)", "SK", nil,
		map[string]types.AttributeValue{":dep": s("DEP#")}, func(it map[string]types.AttributeValue) error {
			if sk, _ := it["SK"].(*types.AttributeValueMemberS); sk != nil {
				if b := depBaseOf(sk.Value); b != "" {
					seen[b] = true
				}
			}
			return nil
		})
	if err != nil {
		return err
	}
	bases := make([]string, 0, len(seen))
	for b := range seen {
		bases = append(bases, b)
	}
	if err := d.indexPackages(ctx, bases); err != nil {
		return err
	}
	return d.put(ctx, item{PK: pkgMarkerPK, SK: "BUILT"}, nil)
}

// dropPackageIndex removes base's entry, and forgets that this process wrote it.
func (d *Dynamo) dropPackageIndex(ctx context.Context, base string) error {
	d.pkgIndexed.Delete(base)
	_, err := d.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &d.table,
		Key: map[string]types.AttributeValue{"PK": s(pkgIndexPK), "SK": s(base)}})
	return err
}
