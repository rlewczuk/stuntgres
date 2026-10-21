package index

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// ExclusionShapeEntries projects the statement's surviving writes on parent
// into the admitted entries of every exclusion index of the new shape.
func ExclusionShapeEntries(evaluation *exec.Context, parent *catalog.Relation, view catalog.Read, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) (map[ids.ObjectAddress][]ExclusionEntry, error) {
	entries := make(map[ids.ObjectAddress][]ExclusionEntry, len(parent.Constraints.Exclusion))
	for _, c := range parent.Constraints.Exclusion {
		idx, ok := view.Relation(c.Index)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := BindExclusionOrdering(parent, idx.SQLIndex)
		if err != nil {
			return nil, err
		}
		var preparation ExecutionPreparation
		for _, row := range writes {
			if UniqueRowDeleted(row.Row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if idx.SQLIndex.RequiresInsertionOutput() {
				output, ok := row.IndexOutputs[idx.Address()]
				if !ok || !OutputMatches(output, idx) {
					return nil, fmt.Errorf("stuntgres: exclusion shape lost produced index output")
				}
				if !output.Member {
					continue
				}
				if idx.SQLIndex.Method == catalog.HashMethod {
					if output.Key.IsNull() {
						continue
					}
					keys = []sqltype.Value{output.Key}
				} else {
					keys = output.Keys
				}
			} else {
				matches, err := preparation.PredicateMatches(evaluation, parent, idx.SQLIndex, row.Values, &ordering, nil)
				if err != nil {
					return nil, err
				}
				if !matches {
					continue
				}
				if err := preparation.PrepareKeys(evaluation, parent, idx.SQLIndex, &ordering, nil); err != nil {
					return nil, err
				}
				keys = make([]sqltype.Value, len(idx.SQLIndex.Keys))
				if err := ordering.FillKeys(row.Values, keys, evaluation); err != nil {
					return nil, err
				}
			}
			if UniqueHasNull(keys) {
				continue
			}
			entries[idx.Address()] = append(entries[idx.Address()], ExclusionEntry{Row: row, Keys: keys, Admitted: true})
		}
	}
	return entries, nil
}

// ReplaceShape replaces the projected entries of original's
// exclusion indexes by entries of the new shape and ends the rows the
// statement replaced or deleted.
func (p *ExclusionProjection) ReplaceShape(original, parent *catalog.Relation, entries map[ids.ObjectAddress][]ExclusionEntry, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) {
	for _, c := range original.Constraints.Exclusion {
		delete(p.Entries, c.Index)
	}
	for idx, rows := range entries {
		p.Entries[idx] = rows
	}
	ends := maps.Clone(p.Ended[parent.Address()])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	for _, row := range writes {
		if row.Base != 0 {
			ends[row.Row] = row.Base
		}
	}
	if deletes != nil {
		for id, candidate := range deletes.Rows {
			ends[id] = candidate.Version
		}
	}
	p.Ended[parent.Address()] = ends
}
