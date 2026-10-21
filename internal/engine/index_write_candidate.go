package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The insertion producer has already materialized every checked index in order.
// Staging validates exact produced-data authority and never invokes index SQL.
func (s *StatementScope) validateIndexCandidates(ref ids.ObjectRef, batch *index.WriteBatch) error {
	if len(batch.Values) == 0 {
		return nil
	}
	binding := s.uniqueBinding
	if binding == nil || binding.Ref != ref {
		var err error
		binding, err = s.bindUniqueWrite(ref)
		if err != nil {
			return err
		}
	}
	if !binding.Checked {
		return nil
	}
	for i := range batch.Values {
		outputs := batch.Candidate(i).Indexes
		for j := range binding.Keys {
			idx := binding.Keys[j].Index
			if output, exists := outputs[idx.Address()]; !exists || !index.OutputMatches(output, idx) {
				return ids.ErrCatalogReplaced
			}
		}
		for j := range binding.Insertion {
			insertion := &binding.Insertion[j]
			if insertion.Unique == nil {
				if output, exists := outputs[insertion.Index.Address()]; !exists || !index.OutputMatches(output, insertion.Index) {
					return ids.ErrCatalogReplaced
				}
			}
		}
	}
	return nil
}

func (s *StatementScope) existingUniqueOutput(bound *index.UniqueBoundKey, row storage.RowVersion, evaluation *exec.Context, purpose index.ProbePurpose) (bool, []sqltype.Value, error) {
	idx := bound.Index
	read := func(output storage.IndexOutput) (bool, []sqltype.Value, error) {
		if !index.OutputMatches(output, idx) {
			return false, nil, ids.ErrCatalogReplaced
		}
		if idx.SQLIndex.Method == catalog.HashMethod && output.Member {
			bound.Scratch[0] = output.Key
			return true, bound.Scratch, nil
		}
		return output.Member, output.Keys, nil
	}
	if output, exists := row.IndexOutputs[idx.Address()]; exists {
		return read(output)
	}
	identity := index.RowIdentity{Row: row.Row, Version: row.Version}
	if output, exists := s.indexesOrZero().ExistingOutputs[idx.Address()][identity]; exists {
		return read(output)
	}
	if !idx.SQLIndex.HasRuntimeDomainConversion() && purpose != index.ProbeRevalidation {
		member, err := bound.Preparation.PredicateMatches(evaluation, bound.Parent, idx.SQLIndex, row.Values, &bound.Ordering, nil)
		if err != nil {
			return false, nil, err
		}
		if purpose == index.ProbeRuntime {
			if !member {
				return false, nil, nil
			}
			// The active proposal owns bound.key throughout this probe.
			// Existing-row bucket population must not overwrite that key.
			err := bound.FillKeys(row.Values, bound.Scratch, evaluation)
			return true, bound.Scratch, err
		}
		output := storage.IndexOutput{Index: idx, Member: member}
		if member {
			if idx.SQLIndex.Method == catalog.HashMethod {
				if err := bound.FillKeys(row.Values, bound.Scratch, evaluation); err != nil {
					return false, nil, err
				}
				output.Key = bound.Scratch[0]
			} else {
				output.Keys = make([]sqltype.Value, len(bound.Ordering.Keys))
				if err := bound.FillKeys(row.Values, output.Keys, evaluation); err != nil {
					return false, nil, err
				}
			}
		}
		s.retainExistingIndexOutput(idx, row, output)
		return read(output)
	}
	if purpose == index.ProbeRevalidation && row.Born == 0 {
		return false, nil, ids.ErrCatalogReplaced
	}
	root, err := s.writeHost().ExistingIndexRoot(idx)
	if err != nil {
		return false, nil, err
	}
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
	err = bound.Ordering.FillKeys(row.Values, bound.Scratch, evaluation)
	return true, bound.Scratch, err
}

func (s *StatementScope) matchesExistingUnique(bound *index.UniqueBoundKey, row storage.RowVersion, evaluation *exec.Context, key []sqltype.Value, purpose index.ProbePurpose) (bool, error) {
	if purpose == index.ProbeRuntime && !bound.Index.SQLIndex.HasRuntimeDomainConversion() && row.IndexOutputs == nil {
		return bound.Matches(evaluation, row.Values, key)
	}
	member, produced, err := s.existingUniqueOutput(bound, row, evaluation, purpose)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.Ordering.CompareKeys(evaluation.Context, storage.IndexEntry{Keys: produced}, storage.IndexEntry{Keys: key})
	return cmp == 0, err
}
