package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type rowLockNode struct {
	querySchema
	child LogicalNode
	mode  relationLockMode
}

func queryRowMode(clause string) relationLockMode {
	switch clause {
	case "FOR KEY SHARE":
		return keyShare
	case "FOR SHARE":
		return rowShare
	case "FOR NO KEY UPDATE":
		return noKeyUpdate
	case "FOR UPDATE":
		return rowUpdate
	}
	panic("invalid SELECT row lock mode")
}

func appendQueryOccurrences(dst []int, source *querySource) []int {
	if source.input != nil {
		return appendQueryOccurrences(dst, source.input)
	}
	if source.lineage != nil {
		return appendQueryOccurrences(dst, source.lineage.stage)
	}
	if source.singleton {
		return dst
	}
	if source.materialized {
		return append(dst, source.occurrences...)
	}
	return append(dst, source.occurrence)
}
func appendQueryProvenance(dst []int, source *querySource, index int) []int {
	if source.input != nil {
		return appendQueryProvenance(dst, source.input, index)
	}
	if source.lineage != nil {
		lineage := source.lineage
		lineage.currentIndex = index
		if lineage.cacheWidth != 0 {
			occurrence := index
			if lineage.cacheOccurrences != nil {
				occurrence = lineage.cacheOccurrences[index]
			}
			lineage.currentOrder = lineage.orderValues[occurrence*lineage.cacheWidth : (occurrence+1)*lineage.cacheWidth]
		}
		if source.lineage.selection != nil {
			index = source.lineage.selection[index]
		}
		return appendQueryProvenance(dst, source.lineage.stage, index)
	}
	if source.singleton {
		return dst
	}
	if source.materialized {
		width := len(source.occurrences)
		return append(dst, source.provenance[index*width:(index+1)*width]...)
	}
	return append(dst, index)
}

// The source manifest, not target-only DML authority or a re-resolved name,
// identifies each grant. Waiting shares the existing journal/deadline machinery.
func (s *StatementScope) acquireQueryRow(leaf queryLeaf, row RowID, mode relationLockMode) error {
	if err := s.active(); err != nil {
		return err
	}
	if s.ticket == nil || s.ticket.ref != s.txRef || s.txRef.attachment != s.owner || s.txRef.session != s.op.session.token || row == 0 || leaf.storage == nil {
		return fmt.Errorf("stuntgres: invalid query row acquisition authority")
	}
	e := s.op.session.Engine()
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: leaf.ref, row: row}, mode, policy)
	if err != nil {
		return err
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		if err = request.wait(); err != nil {
			return err
		}
		if s.implicit {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = currentQuerySourceLocked(e, e.sequenceCatalogLocked(s.owner.sequences), leaf)
	return err
}

func (s *StatementScope) latestQueryRow(leaf queryLeaf, candidate rowVersion, hint int, mode relationLockMode) (rowVersion, bool, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := currentQuerySourceLocked(e, e.sequenceCatalogLocked(s.owner.sequences), leaf); err != nil {
		return rowVersion{}, false, err
	}
	store := leaf.storage
	rows := store.versions
	if candidate.born == 0 {
		rows = s.ownWrites[leaf.ref.Address]
	}
	current, _, found := rowByID(rows, candidate.row, hint)
	if !found {
		return rowVersion{}, false, fmt.Errorf("stuntgres: invalid query row predecessor")
	}
	present := liveVersion(current, e.commitSeq, store.ends, s.ownDeletes[leaf.ref.Address])
	if s.owner.isolation == RepeatableRead && current.born != 0 {
		changed := current.born > s.owner.frontier
		if store.ends != nil {
			end, ok := store.ends.rows[current.row]
			changed = changed || ok && end.version == current.version && end.end > s.owner.frontier
		}
		if changed {
			// Native UPDATE is keyless NO KEY UPDATE. KEY SHARE protects the
			// stable RowID's live chain without advancing RR's snapshot values.
			// A deletion must still serialize, including replacement then delete.
			if mode == keyShare && present && current.version != candidate.version {
				return candidate, true, nil
			}
			return rowVersion{}, false, sqlError("40001", "could not serialize access due to concurrent update")
		}
	}
	return current, present, nil
}

func recheckScratchWidth(node LogicalNode) int {
	switch n := node.(type) {
	case *sourceNode:
		return 0
	case *joinNode:
		return len(n.Schema()) + recheckScratchWidth(n.left) + recheckScratchWidth(n.right)
	case *filterNode:
		return recheckScratchWidth(n.child)
	case *sortNode:
		return recheckScratchWidth(n.child)
	}
	panic("invalid locking recheck tree")
}

// Rebuild only the frozen combination using the original bound frame layout.
// Sort is deliberately ignored: PostgreSQL orders before row arbitration.
func recheckQueryRow(node LogicalNode, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int) ([]sqltype.Value, bool, error) {
	switch n := node.(type) {
	case *sourceNode:
		return rows[n.occurrence], true, nil
	case *sortNode:
		row, ok, err := recheckQueryRow(n.child, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		if err := refreshLockedOrder(n, row, evaluation); err != nil {
			return nil, false, err
		}
		return row, true, nil
	case *filterNode:
		row, ok, err := recheckQueryRow(n.child, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		matches, err := updateMatches(evaluation, n.predicate)
		return row, matches, err
	case *joinNode:
		left, ok, err := recheckQueryRow(n.left, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		right, ok, err := recheckQueryRow(n.right, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		copy(frame, left)
		copy(frame[n.leftWidth:], right)
		evaluation.Row = frame
		matches, err := updateMatches(evaluation, n.predicate)
		if err != nil || !matches {
			return nil, matches, err
		}
		for i, expr := range n.merges {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			frame[n.leftWidth+n.rightWidth+i] = value
		}
		return frame, true, nil
	}
	return nil, false, fmt.Errorf("stuntgres: invalid locking recheck tree")
}

func openRowLockNode(ctx context.Context, state *queryState, n *rowLockNode) (indexedStage, error) {
	if state.deferLocks {
		return openQueryNode(ctx, state, n.child)
	}
	scope := state.source.scope
	if scope == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing row locking scope")
	}
	// Relation-kind eligibility is execution-only, even for an empty result.
	for _, leaf := range scope.querySources {
		if leaf.relation.relationKind == sequenceRelation {
			return indexedStage{}, sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", leaf.relation.name))
		}
		if leaf.relation.relationKind == catalogRelation {
			return indexedStage{}, sqlError("0A000", "row locking is not supported for virtual catalog relations")
		}
	}
	state.locking = true
	child, err := openQueryNode(ctx, state, n.child)
	state.locking = false
	if err != nil {
		return indexedStage{}, err
	}
	defer child.Close()
	joined := child.source.materialized
	out := child.source
	var selection []int
	var cacheOccurrences []int
	if child.cacheWidth != 0 {
		cacheOccurrences = make([]int, 0, child.count())
	}
	if joined {
		selection = make([]int, 0, child.count())
	} else {
		out = &querySource{anonymous: make([][]sqltype.Value, 0, child.count())}
	}
	var singleLeaf [1]*querySource
	leaves := state.source.sources
	if leaves == nil {
		singleLeaf[0] = state.source
		leaves = singleLeaf[:]
	}
	var singleRow [1][]sqltype.Value
	currentRows := singleRow[:]
	if len(leaves) > 1 {
		currentRows = make([][]sqltype.Value, len(leaves))
	}
	scratch := make([]sqltype.Value, recheckScratchWidth(n.child))
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: scope, Input: state.input, Output: state.output}
	// child.count/index are frozen before the first wait; no resumed source scan.
	for i := range child.count() {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		index := child.index(i)
		live := true
		changed := false
		count := 1
		if child.source.materialized {
			count = len(child.source.occurrences)
		}
		for j := range count {
			occurrence, visible := child.source.occurrence, index
			if child.source.materialized {
				occurrence = child.source.occurrences[j]
				visible = child.source.provenance[index*count+j]
			}
			source := leaves[occurrence]
			raw := source.rawIndex(visible)
			candidate := source.versionAt(raw)
			hint := raw
			if raw >= len(source.committed) {
				hint -= len(source.committed)
			}
			leaf := scope.querySources[occurrence]
			if err := scope.acquireQueryRow(leaf, candidate.row, n.mode); err != nil {
				return indexedStage{}, err
			}
			current, present, err := scope.latestQueryRow(leaf, candidate, hint, n.mode)
			if err != nil {
				return indexedStage{}, err
			}
			live = live && present
			changed = changed || current.version != candidate.version
			currentRows[occurrence] = current.values
		}
		if !live {
			continue
		}
		row := child.source.row(index)
		if changed {
			offset := 0
			evaluation.orderValues = child.orderRow(i)
			var ok bool
			row, ok, err = recheckQueryRow(n.child, currentRows, &evaluation, scratch, &offset)
			if err != nil {
				return indexedStage{}, err
			}
			if !ok {
				continue
			}
		}
		if child.cacheWidth != 0 {
			cacheOccurrences = append(cacheOccurrences, child.orderOccurrence(i))
		}
		if joined {
			if changed {
				copy(child.source.row(index), row)
			}
			selection = append(selection, index)
		} else {
			out.anonymous = append(out.anonymous, row)
		}
	}
	return indexedStage{source: out, selection: selection, schema: n.Schema(), orderValues: child.orderValues, cacheWidth: child.cacheWidth, cacheOccurrences: cacheOccurrences}, nil
}
