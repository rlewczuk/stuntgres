package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func (r foreignKeyRows) CommittedLive(v storage.RowVersion) bool {
	return r.Shadow[v.Row] == 0 && visibility.LiveVersion(v, r.Frontier, r.Ends, r.PriorDeletes) && visibility.LiveVersion(v, r.Frontier, r.Ends, r.CurrentDeletes)
}
func PrepareForeignKeyChild(ctx context.Context, comparisons []foreignKeyComparison, child, key []sqltype.Value, input sqltype.InputEnvironment) error {
	for i, comparison := range comparisons {
		var err error
		key[i], err = comparison.Comparison.PrepareChild(ctx, child[comparison.Child], input)
		if err != nil {
			return err
		}
	}
	return nil
}

// probeForeignKeyIndex borrows the selected logical index's immutable ordered
// entries. Compare owns the selected family and child input semantics; it need
// not construct a narrowed parent-width key. Stop is a successful visit result.
func ProbeForeignKeyIndex(root *storage.IndexRoot, compare func(storage.IndexEntry) (int, error), visit func(storage.IndexEntry) (bool, error)) error {
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

func ForeignKeyIndexOrder(ctx context.Context, index *catalog.Relation, comparisons []foreignKeyComparison, entry storage.IndexEntry, child []sqltype.Value, input sqltype.InputEnvironment) (int, error) {
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
		order, err := comparison.Comparison.ComparePrepared(ctx, parent, child[i], input)
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

func FindForeignKeyParent(ctx context.Context, idx *catalog.Relation, comparisons []foreignKeyComparison, rows foreignKeyRows, child []sqltype.Value, input sqltype.InputEnvironment) (storage.RowVersion, storage.IndexEntry, error) {
	// Statement-final overlays are not committed index entries. Their shadow
	// identities hide replaced committed rows and earlier transaction versions.
	overlay := rows
	overlay.Committed = nil
	var matching storage.RowVersion
	if err := overlay.Each(func(parent storage.RowVersion) (bool, error) {
		match := true
		var err error
		for i, comparison := range comparisons {
			if parent.Values[comparison.Parent].IsNull() {
				match = false
				break
			}
			var order int
			order, err = comparison.Comparison.ComparePrepared(ctx, parent.Values[comparison.Parent], child[i], input)
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
	err := ProbeForeignKeyIndex(rows.Root, func(entry storage.IndexEntry) (int, error) {
		return ForeignKeyIndexOrder(ctx, idx, comparisons, entry, child, input)
	}, func(entry storage.IndexEntry) (bool, error) {
		parent, err := index.ConflictEntryVersion(rows.Committed, entry)
		if err != nil {
			return false, err
		}
		if !rows.CommittedLive(parent) {
			return false, nil
		}
		matching, locator = parent, entry
		return true, nil
	})
	return matching, locator, err
}
