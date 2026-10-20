package engine

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// A prospective row owns separate precheck and insertion events. Exclusion
// admission may reserve its genuine identity after producing the index keys.
type indexWriteCandidate struct {
	values                        []sqltype.Value
	indexes                       map[ids.ObjectAddress]storage.IndexOutput
	precheck                      map[ids.ObjectAddress]storage.IndexOutput
	exclusion                     *storage.RowVersion
	sharedIndexes, sharedPrecheck bool
}

// The physical vector is also the existing SQL producer/RETURNING payload. The
// optional parallel vector preserves occurrence identity (including zero-width
// rows) without allocating index metadata on the plain-column fast path.
type indexWriteBatch struct {
	values     [][]sqltype.Value
	states     []indexWriteState
	actionMode lock.MultiMode // Explicit accepted physical action, never inferred from grant bits.
}

type indexWriteState struct {
	indexes, precheck map[ids.ObjectAddress]storage.IndexOutput
	exclusion         *storage.RowVersion
}

func (b *indexWriteBatch) append(row indexWriteCandidate) {
	b.values = append(b.values, row.values)
	if (row.indexes != nil || row.precheck != nil || row.exclusion != nil) && b.states == nil {
		b.states = make([]indexWriteState, len(b.values)-1, cap(b.values))
	}
	if b.states != nil {
		b.states = append(b.states, indexWriteState{indexes: row.indexes, precheck: row.precheck, exclusion: row.exclusion})
	}
}

func (b indexWriteBatch) candidate(i int) indexWriteCandidate {
	row := indexWriteCandidate{values: b.values[i]}
	if b.states != nil {
		state := b.states[i]
		row.indexes, row.precheck, row.exclusion = state.indexes, state.precheck, state.exclusion
		row.sharedIndexes, row.sharedPrecheck = true, true
	}
	return row
}

func (b indexWriteBatch) prefix(n int) indexWriteBatch {
	b.values = b.values[:n]
	if b.states != nil {
		b.states = b.states[:n]
	}
	return b
}

func (b *indexWriteBatch) set(i int, row indexWriteCandidate) {
	b.values[i] = row.values
	if (row.indexes != nil || row.precheck != nil || row.exclusion != nil) && b.states == nil {
		b.states = make([]indexWriteState, len(b.values))
	}
	if b.states != nil {
		b.states[i] = indexWriteState{indexes: row.indexes, precheck: row.precheck, exclusion: row.exclusion}
	}
}

func (b indexWriteBatch) slice(first, last int) indexWriteBatch {
	b.values = b.values[first:last]
	if b.states != nil {
		b.states = b.states[first:last]
	}
	return b
}

func indexOutputMatches(r storage.IndexOutput, index *catalog.Relation) bool {
	return r.Index != nil && sameIndexKeySemantics(r.Index, index)
}

func (r *indexWriteCandidate) uniqueOutput(bound *uniqueBoundKey, evaluation *exec.Context, precheck bool, purpose indexProbePurpose) (bool, []sqltype.Value, error) {
	index := bound.index
	if purpose == indexProbeRuntime && !index.SQLIndex.HasRuntimeDomainConversion() {
		member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.SQLIndex, r.values, &bound.ordering, nil)
		if err != nil || !member {
			return member, nil, err
		}
		key, err := bound.load(r.values, evaluation)
		return true, key, err
	}
	outputs := &r.indexes
	shared := &r.sharedIndexes
	if precheck {
		outputs = &r.precheck
		shared = &r.sharedPrecheck
	}
	if output, ok := (*outputs)[index.Address()]; ok {
		if !indexOutputMatches(output, index) {
			return false, nil, ids.ErrCatalogReplaced
		}
		if index.SQLIndex.Method == catalog.HashMethod && output.Member {
			bound.scratch[0] = output.Key
			return true, bound.scratch, nil
		}
		return output.Member, output.Keys, nil
	}
	if purpose == indexProbeRevalidation {
		return false, nil, ids.ErrCatalogReplaced
	}
	member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.SQLIndex, r.values, &bound.ordering, nil)
	if err != nil {
		return false, nil, err
	}
	output := storage.IndexOutput{Index: index, Member: member}
	if member {
		if index.SQLIndex.Method == catalog.HashMethod {
			if err := bound.fillKeys(r.values, bound.scratch, evaluation); err != nil {
				return false, nil, err
			}
			output.Key = bound.scratch[0]
		} else {
			output.Keys = make([]sqltype.Value, len(bound.ordering.keys))
			if err := bound.fillKeys(r.values, output.Keys, evaluation); err != nil {
				return false, nil, err
			}
		}
	}
	if *outputs == nil {
		*outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
	} else if *shared {
		*outputs = maps.Clone(*outputs)
	}
	*shared = false
	(*outputs)[index.Address()] = output
	if index.SQLIndex.Method == catalog.HashMethod && member {
		return true, bound.scratch, nil
	}
	return member, output.Keys, nil
}

func (r *indexWriteCandidate) matchesUnique(bound *uniqueBoundKey, evaluation *exec.Context, key []sqltype.Value) (bool, error) {
	if !bound.index.SQLIndex.HasRuntimeDomainConversion() && r.indexes == nil {
		return bound.matches(evaluation, r.values, key)
	}
	member, produced, err := r.uniqueOutput(bound, evaluation, false, indexProbeRevalidation)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.ordering.compareKeys(evaluation.Context, storage.IndexEntry{Keys: produced}, storage.IndexEntry{Keys: key})
	return cmp == 0, err
}

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

func materializeIndexOutput(evaluation *exec.Context, preparation *indexExecutionPreparation, parent, index *catalog.Relation, ordering *indexOrdering, hash *storage.HashBinding, values []sqltype.Value) (storage.IndexOutput, error) {
	if index.SQLIndex.Method != catalog.HashMethod {
		hash = nil
	}
	member, err := preparation.predicateMatches(evaluation, parent, index.SQLIndex, values, ordering, hash)
	output := storage.IndexOutput{Index: index, Member: member}
	if err != nil || !member {
		return output, err
	}
	if index.SQLIndex.Method == catalog.HashMethod {
		err = preparation.prepareKeys(evaluation, parent, index.SQLIndex, nil, hash)
	} else {
		err = preparation.prepareKeys(evaluation, parent, index.SQLIndex, ordering, nil)
	}
	if err != nil {
		return output, err
	}
	if index.SQLIndex.Method == catalog.HashMethod {
		output.Key, err = extractIndexKey(hash.Slot, hash.Expression, values, evaluation)
	} else {
		output.Keys = make([]sqltype.Value, len(ordering.keys))
		err = ordering.fillKeys(values, output.Keys, evaluation)
	}
	return output, err
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
	e := s.op.session.Engine()
	e.mu.Lock()
	current := e.privateCatalogLocked(s.owner.catalog)
	currentIndex, ok := current.Relation(index.Address())
	if !ok || !sameIndexKeySemantics(index, currentIndex) {
		e.mu.Unlock()
		return false, nil, ids.ErrCatalogReplaced
	}
	root := e.storage.IndexRoots[index.Address()]
	if changes := s.owner.catalog; changes != nil && journalIndexesOrZero(changes).uniqueRoots[index.Address()] != nil {
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
