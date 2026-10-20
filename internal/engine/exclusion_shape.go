package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
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
	evaluation := EvalContext{evalStatement: evalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: view}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	for _, c := range parent.Constraints.Exclusion {
		index, ok := view.Relation(c.Index)
		if !ok {
			return fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := bindExclusionOrdering(parent, index.SQLIndex)
		if err != nil {
			return err
		}
		var preparation indexExecutionPreparation
		for _, row := range writes {
			if uniqueRowDeleted(row.Row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if index.SQLIndex.RequiresInsertionOutput() {
				output, ok := row.IndexOutputs[index.Address()]
				if !ok || !indexOutputMatches(output, index) {
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
				matches, err := preparation.predicateMatches(&evaluation, parent, index.SQLIndex, row.Values, &ordering, nil)
				if err != nil {
					return err
				}
				if !matches {
					continue
				}
				if err := preparation.prepareKeys(&evaluation, parent, index.SQLIndex, &ordering, nil); err != nil {
					return err
				}
				keys = make([]sqltype.Value, len(index.SQLIndex.Keys))
				if err := ordering.fillKeys(row.Values, keys, &evaluation); err != nil {
					return err
				}
			}
			if uniqueHasNull(keys) {
				continue
			}
			entries[index.Address()] = append(entries[index.Address()], exclusionEntry{row: row, keys: keys, admitted: true})
		}
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if view.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	for _, c := range original.Constraints.Exclusion {
		delete(p.entries, c.Index)
	}
	for index, rows := range entries {
		p.entries[index] = rows
	}
	ends := maps.Clone(p.ended[parent.Address()])
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
	p.ended[parent.Address()] = ends
	s.installExclusionProjectionLocked(e, p)
	return nil
}
