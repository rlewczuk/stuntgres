package engine

import (
	"context"
	"fmt"
	"maps"
	"stuntgres/internal/sqltype"
)

func validateAddedExclusions(ctx context.Context, auth AuthorizationView, catalog CatalogRead, original, parent *relationDescriptor, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment) (map[string]*logicalIndexRoot, error) {
	var added []*relationDescriptor
	for _, c := range parent.exclusionConstraints {
		old := false
		for _, prior := range original.exclusionConstraints {
			if prior.OID == c.OID && prior.Index == c.Index {
				old = true
				break
			}
		}
		if old {
			continue
		}
		index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing exclusion build index")
		}
		added = append(added, index)
	}
	if len(added) == 0 {
		return nil, nil
	}
	validation := store
	overlay := len(writes) != 0 || deletes != nil && len(deletes.rows) != 0
	if overlay {
		replaced := make(map[RowID]bool, len(writes))
		for _, row := range writes {
			replaced[row.row] = true
		}
		rows := make([]rowVersion, 0, len(store.versions)+len(writes))
		for _, row := range store.versions {
			if !replaced[row.row] && liveVersion(row, CommitSeq(^uint64(0)), store.ends, deletes) {
				rows = append(rows, row)
			}
		}
		for _, row := range writes {
			if !uniqueRowDeleted(row.row, deletes) {
				rows = append(rows, row)
			}
		}
		validation = &relationStorage{versions: rows}
	}
	var roots map[string]*logicalIndexRoot
	if !overlay {
		roots = make(map[string]*logicalIndexRoot, len(added))
	}
	auth.catalog = catalog
	for _, index := range added {
		root, err := buildIndexRoot(ctx, parent, index.sqlIndex, validation)
		if err != nil {
			return nil, err
		}
		if err := validateExclusionRoot(ctx, parent, index, root, schema, output, &auth, true); err != nil {
			return nil, err
		}
		if roots != nil {
			roots[index.name] = root
		}
	}
	return roots, nil
}
func (s *StatementScope) prepareExclusionShape(original, parent *relationDescriptor, catalog CatalogRead, writes []rowVersion, deletes *privateDeletionRoot) error {
	if len(original.exclusionConstraints)+len(parent.exclusionConstraints) == 0 {
		return nil
	}
	entries := make(map[ObjectAddress][]exclusionEntry, len(parent.exclusionConstraints))
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, Input: s.input, Output: s.output, statementScope: s}
	for _, c := range parent.exclusionConstraints {
		index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := bindExclusionOrdering(parent, index.sqlIndex)
		if err != nil {
			return err
		}
		for _, row := range writes {
			if uniqueRowDeleted(row.row, deletes) {
				continue
			}
			matches, err := indexPredicateMatches(&evaluation, index.sqlIndex.predicate, row.values)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			keys := make([]sqltype.Value, len(index.sqlIndex.keys))
			if err := ordering.fillKeys(row.values, keys, &evaluation); err != nil {
				return err
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
