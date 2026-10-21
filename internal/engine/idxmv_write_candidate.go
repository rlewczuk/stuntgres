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
	Values                        []sqltype.Value
	Indexes                       map[ids.ObjectAddress]storage.IndexOutput
	Precheck                      map[ids.ObjectAddress]storage.IndexOutput
	Exclusion                     *storage.RowVersion
	SharedIndexes, SharedPrecheck bool
}

// The physical vector is also the existing SQL producer/RETURNING payload. The
// optional parallel vector preserves occurrence identity (including zero-width
// rows) without allocating index metadata on the plain-column fast path.
type indexWriteBatch struct {
	Values     [][]sqltype.Value
	States     []indexWriteState
	ActionMode lock.MultiMode // Explicit accepted physical action, never inferred from grant bits.
}

type indexWriteState struct {
	Indexes, Precheck map[ids.ObjectAddress]storage.IndexOutput
	Exclusion         *storage.RowVersion
}

func (b *indexWriteBatch) Append(row indexWriteCandidate) {
	b.Values = append(b.Values, row.Values)
	if (row.Indexes != nil || row.Precheck != nil || row.Exclusion != nil) && b.States == nil {
		b.States = make([]indexWriteState, len(b.Values)-1, cap(b.Values))
	}
	if b.States != nil {
		b.States = append(b.States, indexWriteState{Indexes: row.Indexes, Precheck: row.Precheck, Exclusion: row.Exclusion})
	}
}

func (b indexWriteBatch) Candidate(i int) indexWriteCandidate {
	row := indexWriteCandidate{Values: b.Values[i]}
	if b.States != nil {
		state := b.States[i]
		row.Indexes, row.Precheck, row.Exclusion = state.Indexes, state.Precheck, state.Exclusion
		row.SharedIndexes, row.SharedPrecheck = true, true
	}
	return row
}

func (b indexWriteBatch) Prefix(n int) indexWriteBatch {
	b.Values = b.Values[:n]
	if b.States != nil {
		b.States = b.States[:n]
	}
	return b
}

func (b *indexWriteBatch) Set(i int, row indexWriteCandidate) {
	b.Values[i] = row.Values
	if (row.Indexes != nil || row.Precheck != nil || row.Exclusion != nil) && b.States == nil {
		b.States = make([]indexWriteState, len(b.Values))
	}
	if b.States != nil {
		b.States[i] = indexWriteState{Indexes: row.Indexes, Precheck: row.Precheck, Exclusion: row.Exclusion}
	}
}

func (b indexWriteBatch) Slice(first, last int) indexWriteBatch {
	b.Values = b.Values[first:last]
	if b.States != nil {
		b.States = b.States[first:last]
	}
	return b
}

func OutputMatches(r storage.IndexOutput, index *catalog.Relation) bool {
	return r.Index != nil && SameIndexKeySemantics(r.Index, index)
}

func (r *indexWriteCandidate) UniqueOutput(bound *uniqueBoundKey, evaluation *exec.Context, precheck bool, purpose indexProbePurpose) (bool, []sqltype.Value, error) {
	index := bound.Index
	if purpose == ProbeRuntime && !index.SQLIndex.HasRuntimeDomainConversion() {
		member, err := bound.Preparation.PredicateMatches(evaluation, bound.Parent, index.SQLIndex, r.Values, &bound.Ordering, nil)
		if err != nil || !member {
			return member, nil, err
		}
		key, err := bound.Load(r.Values, evaluation)
		return true, key, err
	}
	outputs := &r.Indexes
	shared := &r.SharedIndexes
	if precheck {
		outputs = &r.Precheck
		shared = &r.SharedPrecheck
	}
	if output, ok := (*outputs)[index.Address()]; ok {
		if !OutputMatches(output, index) {
			return false, nil, ids.ErrCatalogReplaced
		}
		if index.SQLIndex.Method == catalog.HashMethod && output.Member {
			bound.Scratch[0] = output.Key
			return true, bound.Scratch, nil
		}
		return output.Member, output.Keys, nil
	}
	if purpose == ProbeRevalidation {
		return false, nil, ids.ErrCatalogReplaced
	}
	member, err := bound.Preparation.PredicateMatches(evaluation, bound.Parent, index.SQLIndex, r.Values, &bound.Ordering, nil)
	if err != nil {
		return false, nil, err
	}
	output := storage.IndexOutput{Index: index, Member: member}
	if member {
		if index.SQLIndex.Method == catalog.HashMethod {
			if err := bound.FillKeys(r.Values, bound.Scratch, evaluation); err != nil {
				return false, nil, err
			}
			output.Key = bound.Scratch[0]
		} else {
			output.Keys = make([]sqltype.Value, len(bound.Ordering.Keys))
			if err := bound.FillKeys(r.Values, output.Keys, evaluation); err != nil {
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
		return true, bound.Scratch, nil
	}
	return member, output.Keys, nil
}

func (r *indexWriteCandidate) MatchesUnique(bound *uniqueBoundKey, evaluation *exec.Context, key []sqltype.Value) (bool, error) {
	if !bound.Index.SQLIndex.HasRuntimeDomainConversion() && r.Indexes == nil {
		return bound.Matches(evaluation, r.Values, key)
	}
	member, produced, err := r.UniqueOutput(bound, evaluation, false, ProbeRevalidation)
	if err != nil || !member {
		return false, err
	}
	cmp, err := bound.Ordering.CompareKeys(evaluation.Context, storage.IndexEntry{Keys: produced}, storage.IndexEntry{Keys: key})
	return cmp == 0, err
}

func MaterializeIndexOutput(evaluation *exec.Context, preparation *indexExecutionPreparation, parent, index *catalog.Relation, ordering *indexOrdering, hash *storage.HashBinding, values []sqltype.Value) (storage.IndexOutput, error) {
	if index.SQLIndex.Method != catalog.HashMethod {
		hash = nil
	}
	member, err := preparation.PredicateMatches(evaluation, parent, index.SQLIndex, values, ordering, hash)
	output := storage.IndexOutput{Index: index, Member: member}
	if err != nil || !member {
		return output, err
	}
	if index.SQLIndex.Method == catalog.HashMethod {
		err = preparation.PrepareKeys(evaluation, parent, index.SQLIndex, nil, hash)
	} else {
		err = preparation.PrepareKeys(evaluation, parent, index.SQLIndex, ordering, nil)
	}
	if err != nil {
		return output, err
	}
	if index.SQLIndex.Method == catalog.HashMethod {
		output.Key, err = extractIndexKey(hash.Slot, hash.Expression, values, evaluation)
	} else {
		output.Keys = make([]sqltype.Value, len(ordering.Keys))
		err = ordering.FillKeys(values, output.Keys, evaluation)
	}
	return output, err
}

// A private version maintains only indexes ready when its statement accepted it.
// Nil is used by already committed/imported logical roots, not public write admission.
func RowMaintainsIndex(row storage.RowVersion, index *catalog.Relation) bool {
	if row.IndexAdmission == nil {
		return true
	}
	admitted, ok := row.IndexAdmission.Relation(index.Address())
	return ok && admitted.SQLIndex != nil && admitted.SQLIndex.Ready && admitted.SQLIndex.Live
}
