package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

func validateExclusionPair(ctx context.Context, parent, index *relationDescriptor, previous, current logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	if previous.row == current.row || uniqueHasNull(previous.keys) || uniqueHasNull(current.keys) {
		return nil
	}
	match, err := exclusionKeysMatch(ctx, index.sqlIndex, previous.keys, current.keys)
	if err != nil || !match {
		return err
	}
	allowed := auth == nil || auth.engine == nil
	var catalog CatalogRead
	if auth != nil {
		catalog = auth.catalog
		if auth.engine != nil {
			ref := catalog.owner.catalog.domain.Ref(catalog.Epoch(), parent.address)
			allowed, err = uniqueKeyDetailAllowed(*auth, ref, index.sqlIndex.keys)
			if err != nil {
				return err
			}
		}
	}
	key, existing := current.keys, previous.keys
	if building {
		key, existing = previous.keys, current.keys
	}
	return exclusionDiagnostic(ctx, parent, index, key, existing, schema, output, allowed, building, catalog)
}
func validateExclusionRoot(ctx context.Context, parent, index *relationDescriptor, root *logicalIndexRoot, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	if root == nil {
		return ctx.Err()
	}
	if root.hash == nil {
		return validateExclusionEntries(ctx, parent, index, root.entries, schema, output, auth, building)
	}
	var leftKey, rightKey [1]sqltype.Value
	for _, bucket := range root.hash.buckets {
		for i, entry := range bucket {
			leftKey[0] = entry.key
			left := logicalIndexEntry{row: entry.row, version: entry.version, keys: leftKey[:]}
			for _, prior := range bucket[:i] {
				rightKey[0] = prior.key
				right := logicalIndexEntry{row: prior.row, version: prior.version, keys: rightKey[:]}
				if err := validateExclusionPair(ctx, parent, index, right, left, schema, output, auth, building); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func validateExclusionEntries(ctx context.Context, parent, index *relationDescriptor, entries []logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	for i, current := range entries {
		start := 0
		if index.sqlIndex.method == btreeMethod {
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
