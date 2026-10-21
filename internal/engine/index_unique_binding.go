package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type uniqueBoundKey struct {
	Index        *catalog.Relation
	Parent       *catalog.Relation
	Preparation  indexExecutionPreparation
	Ordering     indexOrdering
	Grouping     []sqltype.Grouping
	Key, Scratch []sqltype.Value
}

type uniqueWriteBinding struct {
	Ref       ids.ObjectRef
	Parent    *catalog.Relation
	Catalog   *catalog.State
	Keys      []uniqueBoundKey
	KeySlots  []int
	TypeName  func(sqltype.ID) string
	Insertion []indexInsertionBinding
	Checked   bool
}

func (b *uniqueBoundKey) Load(row []sqltype.Value, evaluation *exec.Context) ([]sqltype.Value, error) {
	err := b.FillKeys(row, b.Key, evaluation)
	return b.Key, err
}

func (b *uniqueBoundKey) FillKeys(row, keys []sqltype.Value, evaluation *exec.Context) error {
	if err := b.Preparation.PrepareKeys(evaluation, b.Parent, b.Index.SQLIndex, &b.Ordering, nil); err != nil {
		return err
	}
	return b.Ordering.FillKeys(row, keys, evaluation)
}

func (b *uniqueBoundKey) Matches(evaluation *exec.Context, values, key []sqltype.Value) (bool, error) {
	member, err := b.Preparation.PredicateMatches(evaluation, b.Parent, b.Index.SQLIndex, values, &b.Ordering, nil)
	if err != nil || !member {
		return false, err
	}
	if err := b.FillKeys(values, b.Scratch, evaluation); err != nil {
		return false, err
	}
	cmp, err := b.Ordering.CompareKeys(evaluation.Context, storage.IndexEntry{Keys: key}, storage.IndexEntry{Keys: b.Scratch})
	return cmp == 0, err
}

func UniqueRowExcluded(id ids.RowID, excluded []storage.RowVersion) bool {
	for _, row := range excluded {
		if row.Row == id {
			return true
		}
	}
	return false
}

func UniqueRowDeleted(id ids.RowID, roots ...*visibility.PrivateDeletes) bool {
	for _, root := range roots {
		if root != nil {
			if _, ok := root.Rows[id]; ok {
				return true
			}
		}
	}
	return false
}

func ConflictEntryVersion(versions []storage.RowVersion, entry storage.IndexEntry) (storage.RowVersion, error) {
	if entry.Position < 0 || entry.Position >= len(versions) {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	head := versions[entry.Position]
	if head.Row != entry.Row || head.Version != entry.Version {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	return head, nil
}

func SameRelationRowShape(a, b *catalog.Relation) bool {
	if a == b {
		return true
	}
	if len(a.Columns) != len(b.Columns) || len(a.Attributes) != len(a.Columns) || len(b.Attributes) != len(b.Columns) {
		return false
	}
	for i, column := range a.Columns {
		other := b.Columns[i]
		if a.Attributes[i] != b.Attributes[i] || column.Name != other.Name || !analyze.SameStatementUsage(column.Usage, other.Usage) || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
			return false
		}
	}
	return true
}
