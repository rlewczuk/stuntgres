package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (r foreignKeyRows) committedLive(v rowVersion) bool {
	return r.shadow[v.row] == 0 && liveVersion(v, r.frontier, r.ends, r.priorDeletes) && liveVersion(v, r.frontier, r.ends, r.currentDeletes)
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
func probeForeignKeyIndex(root *logicalIndexRoot, compare func(logicalIndexEntry) (int, error), visit func(logicalIndexEntry) (bool, error)) error {
	if root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	lo, hi := 0, len(root.entries)
	for lo < hi {
		mid := lo + (hi-lo)/2
		order, err := compare(root.entries[mid])
		if err != nil {
			return err
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for at := lo; at < len(root.entries); at++ {
		entry := root.entries[at]
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

func foreignKeyIndexOrder(ctx context.Context, index *catalog.Relation, comparisons []foreignKeyComparison, entry logicalIndexEntry, child []sqltype.Value, input sqltype.InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for i, comparison := range comparisons {
		key := index.SQLIndex.Keys[i]
		parent := entry.keys[i]
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

func findForeignKeyParent(ctx context.Context, index *catalog.Relation, comparisons []foreignKeyComparison, rows foreignKeyRows, child []sqltype.Value, input sqltype.InputEnvironment) (rowVersion, logicalIndexEntry, error) {
	// Statement-final overlays are not committed index entries. Their shadow
	// identities hide replaced committed rows and earlier transaction versions.
	overlay := rows
	overlay.committed = nil
	var matching rowVersion
	if err := overlay.each(func(parent rowVersion) (bool, error) {
		match := true
		var err error
		for i, comparison := range comparisons {
			if parent.values[comparison.parent].IsNull() {
				match = false
				break
			}
			var order int
			order, err = comparison.comparison.ComparePrepared(ctx, parent.values[comparison.parent], child[i], input)
			if err != nil || order != 0 {
				match = false
				break
			}
		}
		if match {
			matching = parent
		}
		return match, err
	}); err != nil || matching.row != 0 {
		return matching, logicalIndexEntry{}, err
	}
	var locator logicalIndexEntry
	err := probeForeignKeyIndex(rows.root, func(entry logicalIndexEntry) (int, error) {
		return foreignKeyIndexOrder(ctx, index, comparisons, entry, child, input)
	}, func(entry logicalIndexEntry) (bool, error) {
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
