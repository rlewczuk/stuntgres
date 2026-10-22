package index

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// ValidatePrivateExpressions evaluates the derived keys and predicates of
// view's ready, live indexes over the staged private rows, so derived-key
// errors surface before the rows are installed rather than at COMMIT. An
// index with a runtime domain conversion materializes its outputs instead;
// the rows that gain one are cloned and written back into staged. Admission
// owns the rows; view and the execution trees are immutable.
func ValidatePrivateExpressions(evaluation *exec.Context, view catalog.Read, staged map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !idx.SQLIndex.Ready || !idx.SQLIndex.Live {
			continue
		}
		rows := staged[idx.SQLIndex.Parent]
		if len(rows) == 0 {
			continue
		}
		if idx.SQLIndex.Predicate == nil {
			derived := false
			for _, key := range idx.SQLIndex.Keys {
				derived = derived || key.Derived != nil
			}
			if !derived {
				continue
			}
		}
		pending := idx.SQLIndex.HasRuntimeDomainConversion()
		var ordering Ordering
		var hash storage.HashBinding
		var preparation ExecutionPreparation
		parent, ok := view.Relation(idx.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if pending {
			var err error
			if idx.SQLIndex.Method == catalog.HashMethod {
				hash, err = BindHashIndex(parent, idx.SQLIndex)
			} else {
				ordering, err = BindIndexKeys(parent, idx.SQLIndex)
			}
			if err != nil {
				return err
			}
		}
		copied := false
		for i, row := range rows {
			if !RowMaintainsIndex(row, idx) {
				continue
			}
			if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, deletes[idx.SQLIndex.Parent]) {
				continue
			}
			if output, ok := row.IndexOutputs[idx.Address()]; ok && OutputMatches(output, idx) {
				// The SQL producer already materialized this exact version's
				// membership/key under the captured index semantics.
				continue
			}
			if pending {
				output, err := MaterializeIndexOutput(evaluation, &preparation, parent, idx, &ordering, &hash, row.Values)
				if err != nil {
					return err
				}
				outputs := maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
				outputs[idx.Address()] = output
				if !copied {
					rows = slices.Clone(rows)
					copied = true
				}
				rows[i].IndexOutputs = outputs
				continue
			}
			matches, err := preparation.PredicateMatches(evaluation, parent, idx.SQLIndex, row.Values, nil, nil)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			if err := preparation.PrepareOrdinaryKeys(evaluation, parent, idx.SQLIndex); err != nil {
				return err
			}
			evaluation.Row = row.Values
			for keyIndex, key := range idx.SQLIndex.Keys {
				if key.Derived == nil {
					continue
				}
				if _, err := preparation.Ordinary[keyIndex].Eval(evaluation); err != nil {
					return err
				}
			}
		}
		if copied {
			staged[idx.SQLIndex.Parent] = rows
		}
	}
	return nil
}
