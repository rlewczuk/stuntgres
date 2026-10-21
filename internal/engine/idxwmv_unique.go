package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// BuildUniqueWriteBinding binds parent's live unique, exclusion and checked
// insertion indexes in view for the writes of ref. The caller adds the
// consumed programs and the write authority.
func BuildUniqueWriteBinding(ref ids.ObjectRef, parent *catalog.Relation, view catalog.Read) (*index.UniqueWriteBinding, error) {
	var err error
	b := &index.UniqueWriteBinding{Ref: ref, Parent: parent, Catalog: view.State()}
	var indexes []*catalog.Relation
	var checkedNonunique []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent.Address() || !idx.SQLIndex.Live {
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
			attribute, node, err := index.CatalogKey(parent, key)
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
	b.Keys = make([]index.UniqueBoundKey, len(indexes))
	for i, idx := range indexes {
		bound := &b.Keys[i]
		bound.Index = idx
		bound.Parent = parent
		if idx.SQLIndex.IsExclusion() {
			bound.Ordering, err = index.BindExclusionOrdering(parent, idx.SQLIndex)
		} else {
			bound.Ordering, err = index.BindIndexOrdering(parent, idx.SQLIndex)
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
			usage, usageErr := index.KeyUsage(parent, key)
			if usageErr != nil {
				return nil, usageErr
			}
			if idx.SQLIndex.Method == catalog.GistMethod {
				continue
			}
			// network_ops owns typed comparison but not generic grouping. Its
			// claims use the existing comparator-based shared bucket.
			if key.Class.Family == "network_ops" {
				continue
			}
			bound.Grouping[j], err = sqltype.BindGrouping(usage)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(checkedNonunique) != 0 {
		b.Insertion = make([]index.InsertionBinding, 0, len(b.Keys)+len(checkedNonunique))
		for i := range b.Keys {
			key := &b.Keys[i]
			b.Insertion = append(b.Insertion, index.InsertionBinding{Index: key.Index, Unique: key})
		}
		for _, idx := range checkedNonunique {
			bound := index.InsertionBinding{Index: idx, Parent: parent}
			if idx.SQLIndex.Method == catalog.HashMethod {
				bound.Hash, err = index.BindHashIndex(parent, idx.SQLIndex)
			} else {
				bound.Ordering, err = index.BindIndexKeys(parent, idx.SQLIndex)
			}
			if err != nil {
				return nil, err
			}
			b.Insertion = append(b.Insertion, bound)
		}
		slices.SortFunc(b.Insertion, func(a, b index.InsertionBinding) int {
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

// UniqueRootStart returns the first entry of an ordered root that is not
// below key: the start of key's equal run.
func UniqueRootStart(ctx context.Context, ordering *index.Ordering, entries []storage.IndexEntry, key []sqltype.Value) (int, error) {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := lo + (hi-lo)/2
		cmp, err := ordering.CompareKeys(ctx, entries[mid], storage.IndexEntry{Keys: key})
		if err != nil {
			return 0, err
		}
		if cmp < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, nil
}

// ExistingRootOutput reads row's output of bound's index from the index root
// (committed or private) the caller captured.
func ExistingRootOutput(bound *index.UniqueBoundKey, root *storage.IndexRoot, row storage.RowVersion, evaluation *exec.Context, purpose index.ProbePurpose) (bool, []sqltype.Value, error) {
	idx := bound.Index
	if root == nil {
		return false, nil, fmt.Errorf("stuntgres: existing index root is missing")
	}
	if purpose == index.ProbeRevalidation {
		if root.Hash != nil {
			if root.Hash.RowEntries != nil {
				entry := root.Hash.RowEntries[row.Row]
				if entry == nil {
					return false, nil, nil
				}
				if entry.Version != row.Version {
					return false, nil, ids.ErrCatalogReplaced
				}
				bound.Scratch[0] = entry.Key
				return true, bound.Scratch, nil
			}
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if entry.Row == row.Row {
						if entry.Version != row.Version {
							return false, nil, ids.ErrCatalogReplaced
						}
						bound.Scratch[0] = entry.Key
						return true, bound.Scratch, nil
					}
				}
			}
		} else {
			if idx.SQLIndex.Predicate != nil && root.Membership[row.Row] != row.Version {
				return false, nil, nil
			}
			if root.RowEntries != nil {
				ordinal, exists := root.RowEntries[row.Row]
				if !exists || ordinal < 0 || ordinal >= len(root.Entries) {
					return false, nil, ids.ErrCatalogReplaced
				}
				entry := root.Entries[ordinal]
				if entry.Row != row.Row || entry.Version != row.Version {
					return false, nil, ids.ErrCatalogReplaced
				}
				return true, entry.Keys, nil
			}
			for _, entry := range root.Entries {
				if entry.Row == row.Row {
					if entry.Version != row.Version {
						return false, nil, ids.ErrCatalogReplaced
					}
					return true, entry.Keys, nil
				}
			}
		}
		return false, nil, nil
	}
	if root.Hash != nil {
		if !idx.SQLIndex.HasRuntimeDomainKeyConversion() {
			if err := bound.Ordering.FillKeys(row.Values, bound.Scratch, evaluation); err != nil {
				return false, nil, err
			}
			if bound.Scratch[0].IsNull() {
				return false, nil, nil
			}
			h, err := root.Hash.Binding.Provider.Hash(evaluation.Context, bound.Scratch[0])
			if err != nil {
				return false, nil, err
			}
			for _, entry := range root.Hash.Buckets[h] {
				if entry.Row == row.Row && entry.Version == row.Version {
					return true, bound.Scratch, nil
				}
			}
			return false, nil, nil
		}
		entry := root.Hash.RowEntries[row.Row]
		if entry == nil {
			return false, nil, nil
		}
		if entry.Row != row.Row || entry.Version != row.Version {
			return false, nil, fmt.Errorf("stuntgres: existing hash key version changed")
		}
		bound.Scratch[0] = entry.Key
		return true, bound.Scratch, nil
	}
	if idx.SQLIndex.Predicate != nil && root.Membership[row.Row] != row.Version {
		return false, nil, nil
	}
	if idx.SQLIndex.HasRuntimeDomainKeyConversion() {
		ordinal, ok := root.RowEntries[row.Row]
		if !ok || ordinal < 0 || ordinal >= len(root.Entries) {
			return false, nil, fmt.Errorf("stuntgres: existing index key identity is missing")
		}
		entry := root.Entries[ordinal]
		if entry.Row != row.Row || entry.Version != row.Version {
			return false, nil, fmt.Errorf("stuntgres: existing index key version changed")
		}
		return true, entry.Keys, nil
	}
	err := bound.Ordering.FillKeys(row.Values, bound.Scratch, evaluation)
	return true, bound.Scratch, err
}

// The mixed lane may need ordinary prior-own keys again after catalog drift.
// Freeze actual probe data by immutable row/version, not CHECK truth or values.
func ScopeRetainExistingOutput(x *index.Scope, idx *catalog.Relation, row storage.RowVersion, output storage.IndexOutput) {
	if x.ExistingOutputs == nil {
		x.ExistingOutputs = make(map[ids.ObjectAddress]map[index.RowIdentity]storage.IndexOutput)
	}
	outputs := x.ExistingOutputs[idx.Address()]
	if outputs == nil {
		outputs = make(map[index.RowIdentity]storage.IndexOutput)
		x.ExistingOutputs[idx.Address()] = outputs
	}
	outputs[index.RowIdentity{Row: row.Row, Version: row.Version}] = output
}
