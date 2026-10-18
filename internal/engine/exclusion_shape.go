package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) prepareExclusionShape(original, parent *relationDescriptor, catalog CatalogRead, writes []rowVersion, deletes *privateDeletionRoot) error {
	if len(original.exclusionConstraints)+len(parent.exclusionConstraints) == 0 {
		return nil
	}
	entries := make(map[ids.ObjectAddress][]exclusionEntry, len(parent.exclusionConstraints))
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, Input: s.input, Output: s.output, statementScope: s, catalog: catalog}
	for _, c := range parent.exclusionConstraints {
		index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := bindExclusionOrdering(parent, index.sqlIndex)
		if err != nil {
			return err
		}
		var preparation indexExecutionPreparation
		for _, row := range writes {
			if uniqueRowDeleted(row.row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if index.sqlIndex.requiresInsertionOutput() {
				output, ok := row.indexOutputs[index.address]
				if !ok || !output.matches(index) {
					return fmt.Errorf("stuntgres: exclusion shape lost produced index output")
				}
				if !output.member {
					continue
				}
				if index.sqlIndex.method == hashMethod {
					if output.key.IsNull() {
						continue
					}
					keys = []sqltype.Value{output.key}
				} else {
					keys = output.keys
				}
			} else {
				matches, err := preparation.predicateMatches(&evaluation, parent, index.sqlIndex, row.values, &ordering, nil)
				if err != nil {
					return err
				}
				if !matches {
					continue
				}
				if err := preparation.prepareKeys(&evaluation, parent, index.sqlIndex, &ordering, nil); err != nil {
					return err
				}
				keys = make([]sqltype.Value, len(index.sqlIndex.keys))
				if err := ordering.fillKeys(row.values, keys, &evaluation); err != nil {
					return err
				}
			}
			if uniqueHasNull(keys) {
				continue
			}
			entries[index.address] = append(entries[index.address], exclusionEntry{row: row, keys: keys, admitted: true})
		}
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if catalog.Epoch() != e.state.epoch {
		return ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	for _, c := range original.exclusionConstraints {
		delete(p.entries, c.Index)
	}
	for index, rows := range entries {
		p.entries[index] = rows
	}
	ends := maps.Clone(p.ended[parent.address])
	if ends == nil {
		ends = make(map[RowID]VersionID)
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
	p.ended[parent.address] = ends
	s.installExclusionProjectionLocked(e, p)
	return nil
}
