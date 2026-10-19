package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func (r foreignKeyRows) committedLive(v storage.RowVersion) bool {
	return r.shadow[v.Row] == 0 && liveVersion(v, r.frontier, r.ends, r.priorDeletes) && liveVersion(v, r.frontier, r.ends, r.currentDeletes)
}
func prepareForeignKeyChild(ctx context.Context, comparisons []foreignKeyComparison, child, key []sqltype.Value, input sqltype.InputEnvironment) error {
	for i, comparison := range comparisons {
		var err error
		key[i], err = comparison.comparison.PrepareChild(ctx, child[comparison.child], input)
		if err != nil {
			return err
		}
	}
	return nil
}

// probeForeignKeyIndex borrows the selected logical index's immutable ordered
// entries. Compare owns the selected family and child input semantics; it need
// not construct a narrowed parent-width key. Stop is a successful visit result.
func probeForeignKeyIndex(root *storage.IndexRoot, compare func(storage.IndexEntry) (int, error), visit func(storage.IndexEntry) (bool, error)) error {
	if root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	lo, hi := 0, len(root.Entries)
	for lo < hi {
		mid := lo + (hi-lo)/2
		order, err := compare(root.Entries[mid])
		if err != nil {
			return err
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for at := lo; at < len(root.Entries); at++ {
		entry := root.Entries[at]
		order, err := compare(entry)
		if err != nil || order != 0 {
			return err
		}
		if stop, err := visit(entry); err != nil || stop {
			return err
		}
	}
	return nil
}

func foreignKeyIndexOrder(ctx context.Context, index *catalog.Relation, comparisons []foreignKeyComparison, entry storage.IndexEntry, child []sqltype.Value, input sqltype.InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for i, comparison := range comparisons {
		key := index.SQLIndex.Keys[i]
		parent := entry.Keys[i]
		if parent.IsNull() {
			if key.NullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		order, err := comparison.comparison.ComparePrepared(ctx, parent, child[i], input)
		if err != nil {
			return 0, err
		}
		if key.Desc {
			order = -order
		}
		if order != 0 {
			return order, nil
		}
	}
	return 0, nil
}

func findForeignKeyParent(ctx context.Context, index *catalog.Relation, comparisons []foreignKeyComparison, rows foreignKeyRows, child []sqltype.Value, input sqltype.InputEnvironment) (storage.RowVersion, storage.IndexEntry, error) {
	// Statement-final overlays are not committed index entries. Their shadow
	// identities hide replaced committed rows and earlier transaction versions.
	overlay := rows
	overlay.committed = nil
	var matching storage.RowVersion
	if err := overlay.each(func(parent storage.RowVersion) (bool, error) {
		match := true
		var err error
		for i, comparison := range comparisons {
			if parent.Values[comparison.parent].IsNull() {
				match = false
				break
			}
			var order int
			order, err = comparison.comparison.ComparePrepared(ctx, parent.Values[comparison.parent], child[i], input)
			if err != nil || order != 0 {
				match = false
				break
			}
		}
		if match {
			matching = parent
		}
		return match, err
	}); err != nil || matching.Row != 0 {
		return matching, storage.IndexEntry{}, err
	}
	var locator storage.IndexEntry
	err := probeForeignKeyIndex(rows.root, func(entry storage.IndexEntry) (int, error) {
		return foreignKeyIndexOrder(ctx, index, comparisons, entry, child, input)
	}, func(entry storage.IndexEntry) (bool, error) {
		parent, err := conflictEntryVersion(rows.committed, entry)
		if err != nil {
			return false, err
		}
		if !rows.committedLive(parent) {
			return false, nil
		}
		matching, locator = parent, entry
		return true, nil
	})
	return matching, locator, err
}
