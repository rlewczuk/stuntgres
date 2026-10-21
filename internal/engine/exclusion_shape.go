package engine

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

func (s *StatementScope) prepareExclusionShape(original, parent *catalog.Relation, view catalog.Read, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	if len(original.Constraints.Exclusion)+len(parent.Constraints.Exclusion) == 0 {
		return nil
	}
	entries := make(map[ids.ObjectAddress][]exclusionEntry, len(parent.Constraints.Exclusion))
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, c := range parent.Constraints.Exclusion {
		index, ok := view.Relation(c.Index)
		if !ok {
			return fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := BindExclusionOrdering(parent, index.SQLIndex)
		if err != nil {
			return err
		}
		var preparation indexExecutionPreparation
		for _, row := range writes {
			if UniqueRowDeleted(row.Row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if index.SQLIndex.RequiresInsertionOutput() {
				output, ok := row.IndexOutputs[index.Address()]
				if !ok || !OutputMatches(output, index) {
					return fmt.Errorf("stuntgres: exclusion shape lost produced index output")
				}
				if !output.Member {
					continue
				}
				if index.SQLIndex.Method == catalog.HashMethod {
					if output.Key.IsNull() {
						continue
					}
					keys = []sqltype.Value{output.Key}
				} else {
					keys = output.Keys
				}
			} else {
				matches, err := preparation.PredicateMatches(&evaluation, parent, index.SQLIndex, row.Values, &ordering, nil)
				if err != nil {
					return err
				}
				if !matches {
					continue
				}
				if err := preparation.PrepareKeys(&evaluation, parent, index.SQLIndex, &ordering, nil); err != nil {
					return err
				}
				keys = make([]sqltype.Value, len(index.SQLIndex.Keys))
				if err := ordering.FillKeys(row.Values, keys, &evaluation); err != nil {
					return err
				}
			}
			if UniqueHasNull(keys) {
				continue
			}
			entries[index.Address()] = append(entries[index.Address()], exclusionEntry{Row: row, Keys: keys, Admitted: true})
		}
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if view.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	p := e.exclusions.ProjectionLocked(s.ticket)
	for _, c := range original.Constraints.Exclusion {
		delete(p.Entries, c.Index)
	}
	for index, rows := range entries {
		p.Entries[index] = rows
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
	s.installExclusionProjectionLocked(e, p)
	return nil
}
