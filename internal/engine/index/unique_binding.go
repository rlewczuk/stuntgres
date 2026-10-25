package index

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type UniqueBoundKey struct {
	Index        *catalog.Relation
	Parent       *catalog.Relation
	Preparation  ExecutionPreparation
	Ordering     Ordering
	Grouping     []sqltype.Grouping
	Key, Scratch []sqltype.Value
}

type UniqueWriteBinding struct {
	Ref                       ids.ObjectRef
	Parent                    *catalog.Relation
	Catalog                   *catalog.State
	Keys                      []UniqueBoundKey
	KeySlots                  []int
	TypeName                  func(sqltype.ID) string
	Insertion                 []InsertionBinding
	Checked                   bool
	ConstraintEffectsRelevant bool // FK/exclusion obligations under Ref/Parent/Catalog authority.
}

func (b *UniqueBoundKey) Load(row []sqltype.Value, evaluation *exec.Context) ([]sqltype.Value, error) {
	err := b.FillKeys(row, b.Key, evaluation)
	return b.Key, err
}

func (b *UniqueBoundKey) FillKeys(row, keys []sqltype.Value, evaluation *exec.Context) error {
	if err := b.Preparation.PrepareKeys(evaluation, b.Parent, b.Index.SQLIndex, &b.Ordering, nil); err != nil {
		return err
	}
	return b.Ordering.FillKeys(row, keys, evaluation)
}

func (b *UniqueBoundKey) Matches(evaluation *exec.Context, values, key []sqltype.Value) (bool, error) {
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

// BuildUniqueWriteBinding binds parent's live unique, exclusion and checked
// insertion indexes in view for the writes of ref. The caller adds the
// consumed programs and the write authority.
func BuildUniqueWriteBinding(ref ids.ObjectRef, parent *catalog.Relation, view catalog.Read) (*UniqueWriteBinding, error) {
	var err error
	b := &UniqueWriteBinding{Ref: ref, Parent: parent, Catalog: view.State(),
		ConstraintEffectsRelevant: len(parent.Constraints.ForeignKeys) != 0 || len(parent.Constraints.Exclusion) != 0}
	var indexes []*catalog.Relation
	var checkedNonunique []*catalog.Relation
	for _, idx := range view.Relations() {
		// Reuse this admission's relation walk for incoming FK relevance.
		// NOT VALID still enforces new writes; standalone unique indexes may
		// support the referenced key, independently of Unique constraints.
		if !b.ConstraintEffectsRelevant {
			for _, constraint := range idx.Constraints.ForeignKeys {
				if constraint.Target == parent.Address() {
					b.ConstraintEffectsRelevant = true
					break
				}
			}
		}
		if idx.SQLIndex == nil || idx.SQLIndex.Parent != parent.Address() || !idx.SQLIndex.Live {
			continue
		}
		if idx.SQLIndex.Ready && idx.SQLIndex.RequiresInsertionOutput() {
			b.Checked = true
			if !idx.SQLIndex.Unique && !idx.SQLIndex.IsExclusion() {
				checkedNonunique = append(checkedNonunique, idx)
			}
		}
		if !idx.SQLIndex.Unique && !idx.SQLIndex.IsExclusion() {
			continue
		}
		// REL18 row-key eligibility is independent of readiness/validity.
		// An expression anywhere excludes the whole index; INCLUDE is payload.
		var slots [32]int
		if len(idx.SQLIndex.Keys) > len(slots) {
			return nil, errors.New("stuntgres: invalid unique index key count")
		}
		simple := true
		for at, key := range idx.SQLIndex.Keys {
			attribute, node, err := CatalogKey(parent, key)
			if err != nil {
				return nil, err
			}
			if node != nil {
				simple = false
				break
			}
			slots[at] = slices.Index(parent.Attributes, attribute)
		}
		if idx.SQLIndex.Unique && simple && idx.SQLIndex.Predicate == nil {
			for _, slot := range slots[:len(idx.SQLIndex.Keys)] {
				if !slices.Contains(b.KeySlots, slot) {
					b.KeySlots = append(b.KeySlots, slot)
				}
			}
		}
		if idx.SQLIndex.Ready {
			indexes = append(indexes, idx)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	b.Keys = make([]UniqueBoundKey, len(indexes))
	for i, idx := range indexes {
		bound := &b.Keys[i]
		bound.Index = idx
		bound.Parent = parent
		if idx.SQLIndex.IsExclusion() {
			bound.Ordering, err = BindExclusionOrdering(view, parent, idx.SQLIndex)
		} else {
			bound.Ordering, err = BindIndexOrdering(view, parent, idx.SQLIndex)
		}
		if err != nil {
			return nil, err
		}
		n := len(idx.SQLIndex.Keys)
		if idx.SQLIndex.Method != catalog.GistMethod {
			bound.Grouping = make([]sqltype.Grouping, n)
		}
		bound.Key, bound.Scratch = make([]sqltype.Value, n), make([]sqltype.Value, n)
		for j, key := range idx.SQLIndex.Keys {
			usage, usageErr := KeyUsage(parent, key)
			if usageErr != nil {
				return nil, usageErr
			}
			if idx.SQLIndex.Method == catalog.GistMethod {
				continue
			}
			// Network datums own comparison, but no generic grouping provider.
			// Custom families use the same comparator-based claim bucket.
			if usage.Type.BaseType().ID() == sqltype.Inet || usage.Type.BaseType().ID() == sqltype.Cidr {
				continue
			}
			bound.Grouping[j], err = sqltype.BindGrouping(usage)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(checkedNonunique) != 0 {
		b.Insertion = make([]InsertionBinding, 0, len(b.Keys)+len(checkedNonunique))
		for i := range b.Keys {
			key := &b.Keys[i]
			b.Insertion = append(b.Insertion, InsertionBinding{Index: key.Index, Unique: key})
		}
		for _, idx := range checkedNonunique {
			bound := InsertionBinding{Index: idx, Parent: parent}
			if idx.SQLIndex.Method == catalog.HashMethod {
				bound.Hash, err = BindHashIndex(view, parent, idx.SQLIndex)
			} else {
				bound.Ordering, err = BindIndexKeys(view, parent, idx.SQLIndex)
			}
			if err != nil {
				return nil, err
			}
			b.Insertion = append(b.Insertion, bound)
		}
		slices.SortFunc(b.Insertion, func(a, b InsertionBinding) int {
			if a.Index.Address().ObjectID < b.Index.Address().ObjectID {
				return -1
			}
			if a.Index.Address().ObjectID > b.Index.Address().ObjectID {
				return 1
			}
			return 0
		})
	}
	return b, nil
}
