package index

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Replacement performs the actual rebuild in admitted owner repair, before the
// target lock. Locked remint and domain transport may only reown a proven exact
// slot vector; they never evaluate expressions or reuse remapped range ordinals.
func (snapshot *Snapshot) transportBrinRoot(ctx context.Context, view catalog.Read, source SnapshotSource, parent, index *catalog.Relation, store *storage.Relation, selected map[RowIdentity]int, collation typedef.CollationRemapper) (*storage.IndexRoot, error) {
	if source.Root.Brin == nil || len(source.Store.Versions) != len(store.Versions) || len(selected) != len(store.Versions) {
		return nil, ids.ErrCatalogReplaced
	}
	for slot, row := range source.Store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if position, ok := selected[RowIdentity{Row: row.Row, Version: row.Version}]; !ok || position != slot {
			return nil, ids.ErrCatalogReplaced
		}
	}
	bindings, err := BindBrinIndex(view, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	next := *source.Root.Brin
	next.Bindings, next.Storage, next.Generation, next.Prefix, next.Ends = bindings, store.IndexIdentity(), store.Generation, len(store.Versions), store.Ends
	next.Ranges = slices.Clone(next.Ranges)
	for ordinal, r := range next.Ranges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.Summarized {
			continue
		}
		r.Keys = slices.Clone(r.Keys)
		for key, s := range r.Keys {
			if !s.HasNonNull {
				continue
			}
			s.Min, err = snapshot.Datum(s.Min, bindings[key].Usage.Type, collation, view.State().ACLItemImport())
			if err != nil {
				return nil, err
			}
			s.Max, err = snapshot.Datum(s.Max, bindings[key].Usage.Type, collation, view.State().ACLItemImport())
			if err != nil {
				return nil, err
			}
			r.Keys[key] = s
		}
		next.Ranges[ordinal] = r
	}
	return &storage.IndexRoot{Brin: &next}, ctx.Err()
}
