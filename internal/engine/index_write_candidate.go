package engine

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// A prospective row owns separate precheck and insertion events. Exclusion
// admission may reserve its genuine identity after producing the index keys.
type indexWriteCandidate struct {
	values                        []sqltype.Value
	indexes                       map[ids.ObjectAddress]indexRowOutput
	precheck                      map[ids.ObjectAddress]indexRowOutput
	exclusion                     *rowVersion
	sharedIndexes, sharedPrecheck bool
}

// The physical vector is also the existing SQL producer/RETURNING payload. The
// optional parallel vector preserves occurrence identity (including zero-width
// rows) without allocating index metadata on the plain-column fast path.
type indexWriteBatch struct {
	values     [][]sqltype.Value
	states     []indexWriteState
	actionMode multiMode // Explicit accepted physical action, never inferred from grant bits.
}

type indexWriteState struct {
	indexes, precheck map[ids.ObjectAddress]indexRowOutput
	exclusion         *rowVersion
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

// This is an already produced index membership/key, not a CHECK result. Only
// immutable descriptor authority and physical index data cross publication.
type indexRowOutput struct {
	index  *relationDescriptor
	member bool
	keys   []sqltype.Value
	key    sqltype.Value // Hash indexes retain one scalar, not a one-element slice.
}

func (r indexRowOutput) matches(index *relationDescriptor) bool {
	return r.index != nil && sameIndexKeySemantics(r.index, index)
}

func (r *indexWriteCandidate) uniqueOutput(bound *uniqueBoundKey, evaluation *EvalContext, precheck bool, purpose indexProbePurpose) (bool, []sqltype.Value, error) {
	index := bound.index
	if purpose == indexProbeRuntime && !index.sqlIndex.hasRuntimeDomainConversion() {
		member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.sqlIndex, r.values, &bound.ordering, nil)
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
	if output, ok := (*outputs)[index.address]; ok {
		if !output.matches(index) {
			return false, nil, ErrCatalogReplaced
		}
		if index.sqlIndex.method == hashMethod && output.member {
			bound.scratch[0] = output.key
			return true, bound.scratch, nil
		}
		return output.member, output.keys, nil
	}
	if purpose == indexProbeRevalidation {
		return false, nil, ErrCatalogReplaced
	}
	member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.sqlIndex, r.values, &bound.ordering, nil)
	if err != nil {
		return false, nil, err
	}
	output := indexRowOutput{index: index, member: member}
	if member {
		if index.sqlIndex.method == hashMethod {
			if err := bound.fillKeys(r.values, bound.scratch, evaluation); err != nil {
				return false, nil, err
			}
			output.key = bound.scratch[0]
		} else {
			output.keys = make([]sqltype.Value, len(bound.ordering.keys))
			if err := bound.fillKeys(r.values, output.keys, evaluation); err != nil {
				return false, nil, err
			}
		}
	}
	if *outputs == nil {
		*outputs = make(map[ids.ObjectAddress]indexRowOutput)
	} else if *shared {
		*outputs = maps.Clone(*outputs)
	}
	*shared = false
	(*outputs)[index.address] = output
	if index.sqlIndex.method == hashMethod && member {
		return true, bound.scratch, nil
	}
	return member, output.keys, nil
}

func (r *indexWriteCandidate) matchesUnique(bound *uniqueBoundKey, evaluation *EvalContext, key []sqltype.Value) (bool, error) {
	if !bound.index.sqlIndex.hasRuntimeDomainConversion() && r.indexes == nil {
		return bound.matches(evaluation, r.values, key)
	}
	member, produced, err := r.uniqueOutput(bound, evaluation, false, indexProbeRevalidation)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.ordering.compareKeys(evaluation.Context, logicalIndexEntry{keys: produced}, logicalIndexEntry{keys: key})
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
			if output, exists := outputs[index.address]; !exists || !output.matches(index) {
				return ErrCatalogReplaced
			}
		}
		for j := range binding.insertion {
			insertion := &binding.insertion[j]
			if insertion.unique == nil {
				if output, exists := outputs[insertion.index.address]; !exists || !output.matches(insertion.index) {
					return ErrCatalogReplaced
				}
			}
		}
	}
	return nil
}

func materializeIndexOutput(evaluation *EvalContext, preparation *indexExecutionPreparation, parent, index *relationDescriptor, ordering *indexOrdering, hash *hashIndexBinding, values []sqltype.Value) (indexRowOutput, error) {
	if index.sqlIndex.method != hashMethod {
		hash = nil
	}
	member, err := preparation.predicateMatches(evaluation, parent, index.sqlIndex, values, ordering, hash)
	output := indexRowOutput{index: index, member: member}
	if err != nil || !member {
		return output, err
	}
	if index.sqlIndex.method == hashMethod {
		err = preparation.prepareKeys(evaluation, parent, index.sqlIndex, nil, hash)
	} else {
		err = preparation.prepareKeys(evaluation, parent, index.sqlIndex, ordering, nil)
	}
	if err != nil {
		return output, err
	}
	if index.sqlIndex.method == hashMethod {
		output.key, err = extractIndexKey(hash.slot, hash.expression, values, evaluation)
	} else {
		output.keys = make([]sqltype.Value, len(ordering.keys))
		err = ordering.fillKeys(values, output.keys, evaluation)
	}
	return output, err
}

func (s *StatementScope) existingUniqueOutput(bound *uniqueBoundKey, row rowVersion, evaluation *EvalContext, purpose indexProbePurpose) (bool, []sqltype.Value, error) {
	index := bound.index
	read := func(output indexRowOutput) (bool, []sqltype.Value, error) {
		if !output.matches(index) {
			return false, nil, ErrCatalogReplaced
		}
		if index.sqlIndex.method == hashMethod && output.member {
			bound.scratch[0] = output.key
			return true, bound.scratch, nil
		}
		return output.member, output.keys, nil
	}
	if output, exists := row.indexOutputs[index.address]; exists {
		return read(output)
	}
	identity := indexRowIdentity{row: row.row, version: row.version}
	if output, exists := s.existingIndexOutputs[index.address][identity]; exists {
		return read(output)
	}
	if !index.sqlIndex.hasRuntimeDomainConversion() && purpose != indexProbeRevalidation {
		member, err := bound.preparation.predicateMatches(evaluation, bound.parent, index.sqlIndex, row.values, &bound.ordering, nil)
		if err != nil {
			return false, nil, err
		}
		if purpose == indexProbeRuntime {
			if !member {
				return false, nil, nil
			}
			// The active proposal owns bound.key throughout this probe.
			// Existing-row bucket population must not overwrite that key.
			err := bound.fillKeys(row.values, bound.scratch, evaluation)
			return true, bound.scratch, err
		}
		output := indexRowOutput{index: index, member: member}
		if member {
			if index.sqlIndex.method == hashMethod {
				if err := bound.fillKeys(row.values, bound.scratch, evaluation); err != nil {
					return false, nil, err
				}
				output.key = bound.scratch[0]
			} else {
				output.keys = make([]sqltype.Value, len(bound.ordering.keys))
				if err := bound.fillKeys(row.values, output.keys, evaluation); err != nil {
					return false, nil, err
				}
			}
		}
		s.retainExistingIndexOutput(index, row, output)
		return read(output)
	}
	if purpose == indexProbeRevalidation && row.born == 0 {
		return false, nil, ErrCatalogReplaced
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	current := e.privateCatalogLocked(s.owner.catalog)
	currentIndex, ok := current.state.descriptors[index.address].(*relationDescriptor)
	if !ok || !sameIndexKeySemantics(index, currentIndex) {
		e.mu.Unlock()
		return false, nil, ErrCatalogReplaced
	}
	root := e.indexRoots[index.address]
	if changes := s.owner.catalog; changes != nil && changes.uniqueRoots[index.address] != nil {
		root = changes.uniqueRoots[index.address]
	}
	e.mu.Unlock()
	if root == nil {
		return false, nil, fmt.Errorf("stuntgres: existing index root is missing")
	}
	if purpose == indexProbeRevalidation {
		if root.hash != nil {
			if root.hash.rowEntries != nil {
				entry := root.hash.rowEntries[row.row]
				if entry == nil {
					return false, nil, nil
				}
				if entry.version != row.version {
					return false, nil, ErrCatalogReplaced
				}
				bound.scratch[0] = entry.key
				return true, bound.scratch, nil
			}
			for _, bucket := range root.hash.buckets {
				for _, entry := range bucket {
					if entry.row == row.row {
						if entry.version != row.version {
							return false, nil, ErrCatalogReplaced
						}
						bound.scratch[0] = entry.key
						return true, bound.scratch, nil
					}
				}
			}
		} else {
			if index.sqlIndex.predicate != nil && root.membership[row.row] != row.version {
				return false, nil, nil
			}
			if root.rowEntries != nil {
				ordinal, exists := root.rowEntries[row.row]
				if !exists || ordinal < 0 || ordinal >= len(root.entries) {
					return false, nil, ErrCatalogReplaced
				}
				entry := root.entries[ordinal]
				if entry.row != row.row || entry.version != row.version {
					return false, nil, ErrCatalogReplaced
				}
				return true, entry.keys, nil
			}
			for _, entry := range root.entries {
				if entry.row == row.row {
					if entry.version != row.version {
						return false, nil, ErrCatalogReplaced
					}
					return true, entry.keys, nil
				}
			}
		}
		return false, nil, nil
	}
	if root.hash != nil {
		if !index.sqlIndex.hasRuntimeDomainKeyConversion() {
			if err := bound.ordering.fillKeys(row.values, bound.scratch, evaluation); err != nil {
				return false, nil, err
			}
			if bound.scratch[0].IsNull() {
				return false, nil, nil
			}
			h, err := root.hash.binding.provider.Hash(evaluation.Context, bound.scratch[0])
			if err != nil {
				return false, nil, err
			}
			for _, entry := range root.hash.buckets[h] {
				if entry.row == row.row && entry.version == row.version {
					return true, bound.scratch, nil
				}
			}
			return false, nil, nil
		}
		entry := root.hash.rowEntries[row.row]
		if entry == nil {
			return false, nil, nil
		}
		if entry.row != row.row || entry.version != row.version {
			return false, nil, fmt.Errorf("stuntgres: existing hash key version changed")
		}
		bound.scratch[0] = entry.key
		return true, bound.scratch, nil
	}
	if index.sqlIndex.predicate != nil && root.membership[row.row] != row.version {
		return false, nil, nil
	}
	if index.sqlIndex.hasRuntimeDomainKeyConversion() {
		ordinal, ok := root.rowEntries[row.row]
		if !ok || ordinal < 0 || ordinal >= len(root.entries) {
			return false, nil, fmt.Errorf("stuntgres: existing index key identity is missing")
		}
		entry := root.entries[ordinal]
		if entry.row != row.row || entry.version != row.version {
			return false, nil, fmt.Errorf("stuntgres: existing index key version changed")
		}
		return true, entry.keys, nil
	}
	err := bound.ordering.fillKeys(row.values, bound.scratch, evaluation)
	return true, bound.scratch, err
}

func (s *StatementScope) matchesExistingUnique(bound *uniqueBoundKey, row rowVersion, evaluation *EvalContext, key []sqltype.Value, purpose indexProbePurpose) (bool, error) {
	if purpose == indexProbeRuntime && !bound.index.sqlIndex.hasRuntimeDomainConversion() && row.indexOutputs == nil {
		return bound.matches(evaluation, row.values, key)
	}
	member, produced, err := s.existingUniqueOutput(bound, row, evaluation, purpose)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.ordering.compareKeys(evaluation.Context, logicalIndexEntry{keys: produced}, logicalIndexEntry{keys: key})
	return cmp == 0, err
}
