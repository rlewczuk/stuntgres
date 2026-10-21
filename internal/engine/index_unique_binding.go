package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type uniqueBoundKey struct {
	index        *catalog.Relation
	parent       *catalog.Relation
	preparation  indexExecutionPreparation
	ordering     indexOrdering
	grouping     []sqltype.Grouping
	key, scratch []sqltype.Value
}

type uniqueWriteBinding struct {
	ref       ids.ObjectRef
	parent    *catalog.Relation
	catalog   *catalog.State
	keys      []uniqueBoundKey
	keySlots  []int
	typeName  func(sqltype.ID) string
	insertion []indexInsertionBinding
	checked   bool
}

func (b *uniqueBoundKey) load(row []sqltype.Value, evaluation *exec.Context) ([]sqltype.Value, error) {
	err := b.fillKeys(row, b.key, evaluation)
	return b.key, err
}

func (b *uniqueBoundKey) fillKeys(row, keys []sqltype.Value, evaluation *exec.Context) error {
	if err := b.preparation.prepareKeys(evaluation, b.parent, b.index.SQLIndex, &b.ordering, nil); err != nil {
		return err
	}
	return b.ordering.fillKeys(row, keys, evaluation)
}

func (b *uniqueBoundKey) matches(evaluation *exec.Context, values, key []sqltype.Value) (bool, error) {
	member, err := b.preparation.predicateMatches(evaluation, b.parent, b.index.SQLIndex, values, &b.ordering, nil)
	if err != nil || !member {
		return false, err
	}
	if err := b.fillKeys(values, b.scratch, evaluation); err != nil {
		return false, err
	}
	cmp, err := b.ordering.compareKeys(evaluation.Context, storage.IndexEntry{Keys: key}, storage.IndexEntry{Keys: b.scratch})
	return cmp == 0, err
}

func uniqueRowExcluded(id ids.RowID, excluded []storage.RowVersion) bool {
	for _, row := range excluded {
		if row.Row == id {
			return true
		}
	}
	return false
}

func uniqueRowDeleted(id ids.RowID, roots ...*visibility.PrivateDeletes) bool {
	for _, root := range roots {
		if root != nil {
			if _, ok := root.Rows[id]; ok {
				return true
			}
		}
	}
	return false
}

func conflictEntryVersion(versions []storage.RowVersion, entry storage.IndexEntry) (storage.RowVersion, error) {
	if entry.Position < 0 || entry.Position >= len(versions) {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	head := versions[entry.Position]
	if head.Row != entry.Row || head.Version != entry.Version {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	return head, nil
}
