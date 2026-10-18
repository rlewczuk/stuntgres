package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

type rowLockNode struct {
	querySchema
	child     LogicalNode
	marks     []queryRowMark
	firstMode lock.Mode
}

func queryRowMode(clause string) lock.Mode {
	switch clause {
	case "FOR KEY SHARE":
		return lock.ForKeyShare
	case "FOR SHARE":
		return lock.ForShare
	case "FOR NO KEY UPDATE":
		return lock.ForNoKeyUpdate
	case "FOR UPDATE":
		return lock.ForUpdate
	}
	panic("invalid SELECT row lock mode")
}

// absentQueryIndex is an absent physical occurrence, never a real NULL-valued row.
const absentQueryIndex = -1

// Input adapters translate row identity exactly as they translate row cells.
func (source *querySource) inputIndex(index int) int {
	if index != absentQueryIndex && source.windowIndices != nil {
		return source.windowIndices[index]
	}
	return index
}

func queryOccurrenceWidth(source *querySource) int {
	if source.input != nil {
		return queryOccurrenceWidth(source.input)
	}
	if source.lineage != nil {
		return queryOccurrenceWidth(source.lineage.stage)
	}
	if source.singleton {
		return 0
	}
	if source.materialized {
		return len(source.occurrences)
	}
	return 1
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
	if index == absentQueryIndex {
		setQueryLineage(source, index)
		for range queryOccurrenceWidth(source) {
			dst = append(dst, absentQueryIndex)
		}
		return dst
	}
	if source.input != nil {
		return appendQueryProvenance(dst, source.input, source.inputIndex(index))
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
func (s *StatementScope) acquireQueryRow(leaf queryLeaf, row ids.RowID, mark queryRowMark) (bool, error) {
	if err := s.active(); err != nil {
		return false, err
	}
	if s.ticket == nil || s.ticket.Ref() != s.txRef || txOwnerOf(s.txRef) != s.owner || s.txRef.Session() != s.op.session.token || row == 0 || leaf.storage == nil {
		return false, fmt.Errorf("stuntgres: invalid query row acquisition authority")
	}
	e := s.op.session.Engine()
	// Conditional requests reuse the manager's atomic immediate-grant branch:
	// no journal request or timer exists on an unavailable NOWAIT/SKIP probe.
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, NoWait: mark.wait != parser.RowLockBlock, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: leaf.ref, Row: row}, mark.mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		if mark.wait == parser.RowLockSkip {
			return false, nil
		}
		return false, sqlError("55P03", fmt.Sprintf("could not obtain lock on row in relation %q", leaf.relation.Name()))
	}
	if err != nil {
		return false, err
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		if err = request.Wait(); err != nil {
			return false, err
		}
		if s.implicit {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.catalog), s.owner.catalog, leaf)
	if err == nil {
		err = e.assignTransactionXIDLocked(s.txRef)
	}
	return err == nil, err
}

func (s *StatementScope) latestQueryRow(leaf queryLeaf, candidate rowVersion, hint int, mode lock.Mode) (result rowVersion, present bool, err error) {
	e := s.op.session.Engine()
	var physical rowVersion
	defer func() {
		if err != nil || !present {
			return
		}
		kind, failure := lock.MultiLockMode(mode)
		if failure != nil {
			err = failure
			return
		}
		err = s.physicalRowEvent(leaf.ref, physical, kind, 0)
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.catalog), s.owner.catalog, leaf); err != nil {
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
	physical = current
	present = liveVersion(current, e.commitSeq, store.ends, s.ownDeletes[leaf.ref.Address])
	if stableIsolation(s.owner.isolation) && current.born != 0 {
		changed := current.born > s.owner.frontier
		if store.ends != nil {
			end, ok := store.ends.rows[current.row]
			changed = changed || ok && end.version == current.version && end.end > s.owner.frontier
		}
		if changed {
			// KEY SHARE retains RR values only across compatible replacements.
			// A key transition after the frontier remains incompatible even if
			// later updates restore the key or change only payload.
			if mode == lock.ForKeyShare && present && current.version != candidate.version && current.keyChangedAt <= s.owner.frontier {
				return candidate, true, nil
			}
			return rowVersion{}, false, sqlError("40001", "could not serialize access due to concurrent update")
		}
	}
	return current, present, nil
}

func openRowLockNode(ctx context.Context, state *queryState, n *rowLockNode) (indexedStage, error) {
	if state.source.liveScope() == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing row locking scope")
	}
	previous := state.locking
	state.locking = true
	child, err := openQueryNode(ctx, state, n.child)
	state.locking = previous
	if err != nil {
		return indexedStage{}, err
	}
	if !queryHasPhysicalInput(n.child) {
		return child, nil
	}
	if state.source.locks != nil {
		child.pending = appendPendingRowMarks(child.pending, n.marks, state.source)
	}
	return child, nil
}
