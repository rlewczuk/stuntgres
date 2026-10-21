package engine

import (
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

// A private version maintains only indexes ready when its statement accepted it.
// Nil is used by already committed/imported logical roots, not public write admission.
func rowMaintainsIndex(row storage.RowVersion, index *catalog.Relation) bool {
	if row.IndexAdmission == nil {
		return true
	}
	admitted, ok := row.IndexAdmission.Relation(index.Address())
	return ok && admitted.SQLIndex != nil && admitted.SQLIndex.Ready && admitted.SQLIndex.Live
}
