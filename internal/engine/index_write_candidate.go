package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The insertion producer has already materialized every checked index in order.
// Staging validates exact produced-data authority and never invokes index SQL.
func (s *StatementScope) validateIndexCandidates(ref ids.ObjectRef, batch *indexWriteBatch) error {
	if len(batch.values) == 0 {
		return nil
	}
	binding := s.uniqueBinding
	if binding == nil || binding.ref != ref {
		var err error
		binding, err = s.bindUniqueWrite(ref)
		if err != nil {
			return err
		}
	}
	if !binding.checked {
		return nil
	}
	for i := range batch.values {
		outputs := batch.candidate(i).indexes
		for j := range binding.keys {
			index := binding.keys[j].index
			if output, exists := outputs[index.Address()]; !exists || !indexOutputMatches(output, index) {
				return ids.ErrCatalogReplaced
			}
		}
		for j := range binding.insertion {
			insertion := &binding.insertion[j]
			if insertion.unique == nil {
				if output, exists := outputs[insertion.index.Address()]; !exists || !indexOutputMatches(output, insertion.index) {
					return ids.ErrCatalogReplaced
				}
			}
		}
	}
	return nil
}

func (s *StatementScope) existingUniqueOutput(bound *uniqueBoundKey, row storage.RowVersion, evaluation *exec.Context, purpose indexProbePurpose) (bool, []sqltype.Value, error) {
	index := bound.index
	read := func(output storage.IndexOutput) (bool, []sqltype.Value, error) {
		if !indexOutputMatches(output, index) {
			return false, nil, ids.ErrCatalogReplaced
		}
		if index.SQLIndex.Method == catalog.HashMethod && output.Member {
			bound.scratch[0] = output.Key
			return true, bound.scratch, nil
		}
		return output.Member, output.Keys, nil
	}
	if output, exists := row.IndexOutputs[index.Address()]; exists {
		return read(output)
	}
	identity := indexRowIdentity{row: row.Row, version: row.Version}
	if output, exists := s.indexesOrZero().existingOutputs[index.Address()][identity]; exists {
		return read(output)
	}
	if !index.SQLIndex.HasRuntimeDomainConversion() && purpose != indexProbeRevalidation {
		member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.SQLIndex, row.Values, &bound.ordering, nil)
		if err != nil {
			return false, nil, err
		}
		if purpose == indexProbeRuntime {
			if !member {
				return false, nil, nil
			}
			// The active proposal owns bound.key throughout this probe.
			// Existing-row bucket population must not overwrite that key.
			err := bound.fillKeys(row.Values, bound.scratch, evaluation)
			return true, bound.scratch, err
		}
		output := storage.IndexOutput{Index: index, Member: member}
		if member {
			if index.SQLIndex.Method == catalog.HashMethod {
				if err := bound.fillKeys(row.Values, bound.scratch, evaluation); err != nil {
					return false, nil, err
				}
				output.Key = bound.scratch[0]
			} else {
				output.Keys = make([]sqltype.Value, len(bound.ordering.keys))
				if err := bound.fillKeys(row.Values, output.Keys, evaluation); err != nil {
					return false, nil, err
				}
			}
		}
		s.retainExistingIndexOutput(index, row, output)
		return read(output)
	}
	if purpose == indexProbeRevalidation && row.Born == 0 {
		return false, nil, ids.ErrCatalogReplaced
	}
	e := s.engine()
	e.mu.Lock()
	current := e.privateCatalogLocked(s.owner.Catalog)
	currentIndex, ok := current.Relation(index.Address())
	if !ok || !sameIndexKeySemantics(index, currentIndex) {
		e.mu.Unlock()
		return false, nil, ids.ErrCatalogReplaced
	}
	root := e.storage.IndexRoots[index.Address()]
	if changes := s.owner.Catalog; changes != nil && journalIndexesOrZero(changes).uniqueRoots[index.Address()] != nil {
		root = journalIndexesOrZero(changes).uniqueRoots[index.Address()]
	}
	e.mu.Unlock()
	if root == nil {
		return false, nil, fmt.Errorf("stuntgres: existing index root is missing")
	}
	if purpose == indexProbeRevalidation {
		if root.Hash != nil {
			if root.Hash.RowEntries != nil {
				entry := root.Hash.RowEntries[row.Row]
				if entry == nil {
					return false, nil, nil
				}
				if entry.Version != row.Version {
					return false, nil, ids.ErrCatalogReplaced
				}
				bound.scratch[0] = entry.Key
				return true, bound.scratch, nil
			}
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if entry.Row == row.Row {
						if entry.Version != row.Version {
							return false, nil, ids.ErrCatalogReplaced
						}
						bound.scratch[0] = entry.Key
						return true, bound.scratch, nil
					}
				}
			}
		} else {
			if index.SQLIndex.Predicate != nil && root.Membership[row.Row] != row.Version {
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
		if !index.SQLIndex.HasRuntimeDomainKeyConversion() {
			if err := bound.ordering.fillKeys(row.Values, bound.scratch, evaluation); err != nil {
				return false, nil, err
			}
			if bound.scratch[0].IsNull() {
				return false, nil, nil
			}
			h, err := root.Hash.Binding.Provider.Hash(evaluation.Context, bound.scratch[0])
			if err != nil {
				return false, nil, err
			}
			for _, entry := range root.Hash.Buckets[h] {
				if entry.Row == row.Row && entry.Version == row.Version {
					return true, bound.scratch, nil
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
		bound.scratch[0] = entry.Key
		return true, bound.scratch, nil
	}
	if index.SQLIndex.Predicate != nil && root.Membership[row.Row] != row.Version {
		return false, nil, nil
	}
	if index.SQLIndex.HasRuntimeDomainKeyConversion() {
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
	err := bound.ordering.fillKeys(row.Values, bound.scratch, evaluation)
	return true, bound.scratch, err
}

func (s *StatementScope) matchesExistingUnique(bound *uniqueBoundKey, row storage.RowVersion, evaluation *exec.Context, key []sqltype.Value, purpose indexProbePurpose) (bool, error) {
	if purpose == indexProbeRuntime && !bound.index.SQLIndex.HasRuntimeDomainConversion() && row.IndexOutputs == nil {
		return bound.matches(evaluation, row.Values, key)
	}
	member, produced, err := s.existingUniqueOutput(bound, row, evaluation, purpose)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.ordering.compareKeys(evaluation.Context, storage.IndexEntry{Keys: produced}, storage.IndexEntry{Keys: key})
	return cmp == 0, err
}
