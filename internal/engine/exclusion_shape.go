package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) prepareExclusionShape(original, parent *catalog.Relation, view CatalogRead, writes []rowVersion, deletes *privateDeletionRoot) error {
	if len(original.ExclusionConstraints)+len(parent.ExclusionConstraints) == 0 {
		return nil
	}
	entries := make(map[ids.ObjectAddress][]exclusionEntry, len(parent.ExclusionConstraints))
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, Input: s.input, Output: s.output, statementScope: s, catalog: view}
	for _, c := range parent.ExclusionConstraints {
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
			if uniqueRowDeleted(row.row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if index.SQLIndex.RequiresInsertionOutput() {
				output, ok := row.indexOutputs[index.Address()]
				if !ok || !output.matches(index) {
					return fmt.Errorf("stuntgres: exclusion shape lost produced index output")
				}
				if !output.member {
					continue
				}
				if index.SQLIndex.Method == catalog.HashMethod {
					if output.key.IsNull() {
						continue
					}
					keys = []sqltype.Value{output.key}
				} else {
					keys = output.keys
				}
			} else {
				matches, err := preparation.predicateMatches(&evaluation, parent, index.SQLIndex, row.values, &ordering, nil)
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
				if err := ordering.fillKeys(row.values, keys, &evaluation); err != nil {
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
	for _, c := range original.ExclusionConstraints {
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
		if row.base != 0 {
			ends[row.row] = row.base
		}
	}
	if deletes != nil {
		for id, candidate := range deletes.rows {
			ends[id] = candidate.version
		}
	}
	p.ended[parent.Address()] = ends
	s.installExclusionProjectionLocked(e, p)
	return nil
}
