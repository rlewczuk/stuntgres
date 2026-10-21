package engine

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func ValidateExclusionPair(ctx context.Context, parent, index *catalog.Relation, previous, current storage.IndexEntry, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool) error {
	if previous.Row == current.Row || UniqueHasNull(previous.Keys) || UniqueHasNull(current.Keys) {
		return nil
	}
	match, err := ExclusionKeysMatch(ctx, index.SQLIndex, previous.Keys, current.Keys)
	if err != nil || !match {
		return err
	}
	allowed := env == nil // Trusted publication has no SQL principal.
	var catalog catalog.Read
	if view != nil {
		catalog = view.Catalog
		if env != nil {
			ref := catalog.Owner().Domain.Ref(catalog.Epoch(), parent.Address())
			allowed, err = UniqueKeyDetailAllowed(*view, env, ref, index.SQLIndex.Keys)
			if err != nil {
				return err
			}
		}
	}
	key, existing := current.Keys, previous.Keys
	if building {
		key, existing = previous.Keys, current.Keys
	}
	return ExclusionDiagnostic(ctx, parent, index, key, existing, schema, output, allowed, building, catalog)
}
func ValidateExclusionRoot(ctx context.Context, parent, index *catalog.Relation, root *storage.IndexRoot, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool) error {
	if root == nil {
		return ctx.Err()
	}
	if root.Hash == nil {
		return validateExclusionEntries(ctx, parent, index, root.Entries, schema, output, view, env, building)
	}
	var leftKey, rightKey [1]sqltype.Value
	for _, bucket := range root.Hash.Buckets {
		for i, entry := range bucket {
			leftKey[0] = entry.Key
			left := storage.IndexEntry{Row: entry.Row, Version: entry.Version, Keys: leftKey[:]}
			for _, prior := range bucket[:i] {
				rightKey[0] = prior.Key
				right := storage.IndexEntry{Row: prior.Row, Version: prior.Version, Keys: rightKey[:]}
				if err := ValidateExclusionPair(ctx, parent, index, right, left, schema, output, view, env, building); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func validateExclusionEntries(ctx context.Context, parent, index *catalog.Relation, entries []storage.IndexEntry, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool) error {
	for i, current := range entries {
		start := 0
		if index.SQLIndex.Method == catalog.BtreeMethod {
			start = max(0, i-1)
		}
		for _, previous := range entries[start:i] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := ValidateExclusionPair(ctx, parent, index, previous, current, schema, output, view, env, building); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
