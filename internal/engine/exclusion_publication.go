package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func validateExclusionPair(ctx context.Context, parent, index *catalog.Relation, previous, current logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	if previous.Row == current.Row || uniqueHasNull(previous.Keys) || uniqueHasNull(current.Keys) {
		return nil
	}
	match, err := exclusionKeysMatch(ctx, index.SQLIndex, previous.Keys, current.Keys)
	if err != nil || !match {
		return err
	}
	allowed := auth == nil || auth.engine == nil
	var catalog catalog.Read
	if auth != nil {
		catalog = auth.catalog
		if auth.engine != nil {
			ref := catalog.Owner().Domain.Ref(catalog.Epoch(), parent.Address())
			allowed, err = uniqueKeyDetailAllowed(*auth, ref, index.SQLIndex.Keys)
			if err != nil {
				return err
			}
		}
	}
	key, existing := current.Keys, previous.Keys
	if building {
		key, existing = previous.Keys, current.Keys
	}
	return exclusionDiagnostic(ctx, parent, index, key, existing, schema, output, allowed, building, catalog)
}
func validateExclusionRoot(ctx context.Context, parent, index *catalog.Relation, root *logicalIndexRoot, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	if root == nil {
		return ctx.Err()
	}
	if root.Hash == nil {
		return validateExclusionEntries(ctx, parent, index, root.Entries, schema, output, auth, building)
	}
	var leftKey, rightKey [1]sqltype.Value
	for _, bucket := range root.Hash.Buckets {
		for i, entry := range bucket {
			leftKey[0] = entry.Key
			left := logicalIndexEntry{Row: entry.Row, Version: entry.Version, Keys: leftKey[:]}
			for _, prior := range bucket[:i] {
				rightKey[0] = prior.Key
				right := logicalIndexEntry{Row: prior.Row, Version: prior.Version, Keys: rightKey[:]}
				if err := validateExclusionPair(ctx, parent, index, right, left, schema, output, auth, building); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func validateExclusionEntries(ctx context.Context, parent, index *catalog.Relation, entries []logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	for i, current := range entries {
		start := 0
		if index.SQLIndex.Method == catalog.BtreeMethod {
			start = max(0, i-1)
		}
		for _, previous := range entries[start:i] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := validateExclusionPair(ctx, parent, index, previous, current, schema, output, auth, building); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
