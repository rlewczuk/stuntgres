package engine

import (
	"context"
	"errors"

	"stuntgres/internal/sqltype"
)

// Roots are immutable after transfer to a command view or committed storage.
type rowEnd struct {
	version VersionID
	end     CommitSeq
}
type deletionRoot struct{ rows map[RowID]rowEnd }
type deleteCandidate struct {
	ref     ObjectRef
	storage *relationStorage
	row     RowID
	version VersionID
	ordinal int
	own     bool
}
type privateDeletionRoot struct{ rows map[RowID]deleteCandidate }

func liveVersion(v rowVersion, frontier CommitSeq, ends *deletionRoot, own *privateDeletionRoot) bool {
	if own != nil {
		if _, deleted := own.rows[v.row]; deleted {
			return false
		}
	}
	if v.born == 0 {
		return true
	}
	if v.born > frontier {
		return false
	}
	if ends != nil {
		if end, deleted := ends.rows[v.row]; deleted && end.version == v.version && end.end <= frontier {
			return false
		}
	}
	return true
}

func mergeDeletions(previous, staged map[ObjectAddress]*privateDeletionRoot) map[ObjectAddress]*privateDeletionRoot {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ObjectAddress]*privateDeletionRoot, len(previous)+len(staged))
	for a, root := range previous {
		next[a] = root
	}
	for a, root := range staged {
		old := previous[a]
		if old == nil {
			next[a] = root
			continue
		}
		rows := make(map[RowID]deleteCandidate, len(old.rows)+len(root.rows))
		for id, candidate := range old.rows {
			rows[id] = candidate
		}
		for id, candidate := range root.rows {
			rows[id] = candidate
		}
		next[a] = &privateDeletionRoot{rows: rows}
	}
	return next
}

func (s *StatementScope) acquireRow(candidate deleteCandidate) error {
	if err := s.active(); err != nil {
		return err
	}
	if s.ticket == nil || s.ticket.ref != s.txRef || s.txRef.attachment != s.owner || s.txRef.session != s.op.session.token || candidate.ref != s.lockedRef || candidate.storage == nil || candidate.storage != s.lockedStorage || candidate.row == 0 || candidate.version == 0 || candidate.ordinal < 0 {
		return errors.New("stuntgres: invalid row acquisition authority")
	}
	e := s.op.session.Engine()
	policy := relationLockPolicy{timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: candidate.ref, row: candidate.row}, rowUpdate, policy)
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
	if err = s.validateLockedRelation(e); err != nil {
		return err
	}
	if candidate.ref.owner != e.owner || candidate.ref.Epoch != e.state.epoch || candidate.storage != e.storage[candidate.ref.Address] {
		return ErrCatalogReplaced
	}
	return nil
}

// Delete transfers only the original identity, never a value-based substitute.
// A peer committed deletion is a successful skip after arbitration.
func (w *WriteStage) Delete(candidate deleteCandidate) (bool, error) {
	if w == nil || w.self != w || w.scope == nil {
		return false, errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.active(); err != nil {
		return false, err
	}
	manager := s.op.session.Engine().locks
	resource := lockResource{ObjectRef: candidate.ref, row: candidate.row}
	manager.mu.Lock()
	entry := manager.entries[resource]
	held := manager.valid(s.ticket) && entry != nil && entry.holders[s.ticket]&rowUpdate != 0
	manager.mu.Unlock()
	if !held {
		return false, errors.New("stuntgres: deletion requires its row grant")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted || s.owner.ref != s.txRef {
		return false, errors.New("stuntgres: inactive write stage")
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if candidate.ref.owner != e.owner || candidate.ref.Address.SubID != 0 || candidate.row == 0 || candidate.version == 0 {
		return false, errors.New("stuntgres: invalid deletion identity")
	}
	if candidate.ref.Epoch != e.state.epoch || candidate.ref.Epoch != s.txRef.epoch || candidate.storage == nil || candidate.storage != e.storage[candidate.ref.Address] {
		return false, ErrCatalogReplaced
	}
	if s.view == nil {
		return false, errors.New("stuntgres: deletion requires its candidate view")
	}
	data := s.view.read()
	if candidate.ref != s.lockedRef || candidate.storage != s.lockedStorage || candidate.storage != data.storage {
		return false, errors.New("stuntgres: deletion is outside its candidate shape")
	}
	captured := data.committed[candidate.ref.Address]
	if candidate.own {
		captured = data.own[candidate.ref.Address]
	}
	if candidate.ordinal < 0 || candidate.ordinal >= len(captured) || captured[candidate.ordinal].row != candidate.row || captured[candidate.ordinal].version != candidate.version {
		return false, errors.New("stuntgres: deletion is outside its candidate view")
	}
	rows := s.ownWrites[candidate.ref.Address]
	storage := e.storage[candidate.ref.Address]
	if storage == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.own {
		rows = storage.versions
	}
	if candidate.ordinal < 0 || candidate.ordinal >= len(rows) {
		return false, errors.New("stuntgres: invalid deletion ordinal")
	}
	original := rows[candidate.ordinal]
	relation, ok := e.state.descriptors[candidate.ref.Address].(*relationDescriptor)
	if !ok || original.row != candidate.row || original.version != candidate.version || len(original.values) != len(relation.columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !liveVersion(original, e.commitSeq, storage.ends, s.ownDeletes[candidate.ref.Address]) {
		return false, nil
	}
	root := w.deletes[candidate.ref.Address]
	if root != nil {
		if _, duplicate := root.rows[candidate.row]; duplicate {
			return false, errors.New("stuntgres: duplicate staged deletion")
		}
	}
	if w.deletes == nil {
		w.deletes = make(map[ObjectAddress]*privateDeletionRoot)
	}
	if root == nil {
		root = &privateDeletionRoot{rows: make(map[RowID]deleteCandidate)}
		w.deletes[candidate.ref.Address] = root
	}
	root.rows[candidate.row] = candidate
	return true, nil
}

func (s *StatementScope) executeDelete(b binding, view *VisibilityView, parameters []sqltype.Value) (Result, error) {
	source, err := captureQuerySource(s.op.Context(), view, b.ref)
	if err != nil {
		return Result{}, err
	}
	defer source.close()
	return s.deleteSource(b, source, parameters)
}

func (s *StatementScope) deleteSource(b binding, source *querySource, parameters []sqltype.Value) (Result, error) {
	if b.storage == nil || b.storage != s.lockedStorage || b.storage != source.view.read().storage {
		return Result{}, ErrCatalogReplaced
	}
	ctx := s.op.Context()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters}
	var count int64
	for i := range source.count() {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		ordinal := source.rawIndex(i)
		version := source.versionAt(ordinal)
		evaluation.Row = version.values
		if b.predicate != nil {
			value, err := b.predicate.Eval(&evaluation)
			if err != nil {
				return Result{}, err
			}
			truth, err := TruthOf(value)
			if err != nil {
				return Result{}, err
			}
			if truth != True {
				continue
			}
		}
		candidate := deleteCandidate{ref: b.ref, storage: b.storage, row: version.row, version: version.version, ordinal: ordinal}
		if ordinal >= len(source.committed) {
			candidate.own = true
			candidate.ordinal -= len(source.committed)
		}
		if err := s.acquireRow(candidate); err != nil {
			return Result{}, err
		}
		deleted, err := s.Writes().Delete(candidate)
		if err != nil {
			return Result{}, err
		}
		if deleted {
			count++
		}
	}
	return Result{Count: count}, nil
}

// The nil selection path remains allocation-free for undeleted prefixes.
func captureQuerySource(ctx context.Context, view *VisibilityView, ref ObjectRef) (*querySource, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	data := pin.read()
	if data.catalog.owner == nil {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility catalog")
	}
	if _, ok := data.committed[ref.Address]; !ok || ref.owner != data.catalog.owner.owner || ref.Epoch != data.catalog.Epoch() {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility target")
	}
	source := &querySource{view: pin, ref: ref, committed: data.committed[ref.Address], own: data.own[ref.Address]}
	ends, own := data.ends[ref.Address], data.deleted[ref.Address]
	if ends == nil && own == nil {
		return source, nil
	}
	total := len(source.committed) + len(source.own)
	for i := range total {
		if err := ctx.Err(); err != nil {
			source.close()
			return nil, err
		}
		if liveVersion(source.versionAt(i), data.frontier, ends, own) {
			if source.visible != nil {
				source.visible = append(source.visible, i)
			}
		} else if source.visible == nil {
			source.visible = make([]int, i, total)
			for j := range i {
				source.visible[j] = j
			}
		}
	}
	return source, nil
}
