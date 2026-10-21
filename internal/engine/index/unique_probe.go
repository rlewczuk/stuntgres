package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// UniqueRootStart returns the first entry of an ordered root that is not
// below key: the start of key's equal run.
func UniqueRootStart(ctx context.Context, ordering *Ordering, entries []storage.IndexEntry, key []sqltype.Value) (int, error) {
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
func ExistingRootOutput(bound *UniqueBoundKey, root *storage.IndexRoot, row storage.RowVersion, evaluation *exec.Context, purpose ProbePurpose) (bool, []sqltype.Value, error) {
	idx := bound.Index
	if root == nil {
		return false, nil, fmt.Errorf("stuntgres: existing index root is missing")
	}
	if purpose == ProbeRevalidation {
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
