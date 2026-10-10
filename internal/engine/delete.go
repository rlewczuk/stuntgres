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
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
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
	if candidate.ref.owner != e.owner || candidate.ref.Epoch != e.state.epoch || candidate.storage != e.privateStorageLocked(s.owner.catalog, candidate.ref.Address) {
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
	held := manager.valid(s.ticket) && entry != nil && entry.holders[s.ticket].mode&rowUpdate != 0
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
	if candidate.ref.Epoch != e.state.epoch || candidate.ref.Epoch != s.txRef.epoch || candidate.storage == nil || candidate.storage != e.privateStorageLocked(s.owner.catalog, candidate.ref.Address) {
		return false, ErrCatalogReplaced
	}
	if s.view == nil {
		return false, errors.New("stuntgres: deletion requires its candidate view")
	}
	data := s.view.read()
	if candidate.ref != s.lockedRef || candidate.storage != s.lockedStorage || candidate.storage != data.generationAt(candidate.ref.Address) {
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
	storage := e.privateStorageLocked(s.owner.catalog, candidate.ref.Address)
	if storage == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.own {
		rows = storage.versions
	}
	original, slot, found := rowByID(rows, candidate.row, candidate.ordinal)
	relation, ok := e.privateCatalogLocked(s.owner.catalog).state.descriptors[candidate.ref.Address].(*relationDescriptor)
	if !found || !ok || len(original.values) != len(relation.columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !liveVersion(original, e.commitSeq, storage.ends, s.ownDeletes[candidate.ref.Address]) {
		return false, nil
	}
	candidate.version, candidate.ordinal = original.version, slot
	root := w.deletes[candidate.ref.Address]
	if root != nil {
		if _, duplicate := root.rows[candidate.row]; duplicate {
			return false, errors.New("stuntgres: duplicate staged deletion")
		}
	}
	event := [1]serializableWrite{{row: candidate.row, version: candidate.version, base: original.version, baseBorn: original.born, creator: e.serializable.creators[original.version], deletion: true}}
	if err := e.serializableWriteLocked(s, candidate.ref, event[:]); err != nil {
		return false, err
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

func (s *StatementScope) executeDelete(b binding, view *VisibilityView, inputs *querySource, parameters []sqltype.Value) (Result, error) {
	source, err := captureQuerySource(s.op.Context(), view, b.ref)
	if err != nil {
		if inputs != nil {
			inputs.close()
		}
		view.Release()
		return Result{}, err
	}
	defer source.close()
	return s.deleteSource(b, source, inputs, parameters)
}

func (s *StatementScope) deleteSource(b binding, source, inputs *querySource, parameters []sqltype.Value) (Result, error) {
	if inputs != nil {
		defer inputs.close()
	}
	if b.storage == nil || b.storage != s.lockedStorage || b.storage != source.view.read().generationAt(b.ref.Address) {
		return Result{}, ErrCatalogReplaced
	}
	ctx := s.op.Context()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters, statementScope: s, Input: s.input, Output: s.output}
	if inputs != nil {
		if err := selectQuerySources(ctx, inputs); err != nil {
			return Result{}, err
		}
		state := &queryState{source: inputs, parameters: parameters, input: s.input, output: s.output, locking: inputs.locks != nil}
		inputs.scope = s
		evaluation.query = state
	}
	if b.deleteUsing != nil {
		return s.deleteUsingSource(b, source, &evaluation)
	}
	var count int64
	var rows *finalRows
	keep := false
	if b.returning != nil {
		rows = newFinalRows(b.returning.schema, 0)
		rows.output = s.output
		defer func() {
			if !keep {
				rows.Close()
			}
		}()
	}
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
		current, live, err := s.latestDeleteRow(candidate)
		if err != nil {
			return Result{}, err
		}
		if !live {
			continue
		}
		if current.version != candidate.version && b.predicate != nil {
			evaluation.Row = current.values
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
		if err := s.claimUniqueRow(b.ref, current.values); err != nil {
			return Result{}, err
		}
		deleted, err := s.Writes().Delete(candidate)
		if err != nil {
			return Result{}, err
		}
		if deleted {
			count++
			if rows != nil {
				evaluation.Row = current.values
				for output, expr := range b.returning.expressions {
					value, err := expr.Eval(&evaluation)
					if err != nil {
						return Result{}, err
					}
					if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.schema[output].Usage.Type {
						return Result{}, errors.New("stuntgres: invalid finalized DELETE value")
					}
					rows.values = append(rows.values, value)
				}
				rows.count++
			}
		}
	}
	if rows != nil && b.returning.regclassOutput && count != 0 {
		if err := s.op.session.Engine().captureRegclassOutput(s.op, rows); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	keep = true
	if rows != nil {
		return Result{Count: count, Rows: rows}, nil
	}
	return Result{Count: count}, nil
}

// Row arbitration freezes identity, not a stale ordinal or pre-wait predicate.
func (s *StatementScope) latestDeleteRow(candidate deleteCandidate) (rowVersion, bool, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return rowVersion{}, false, err
	}
	store := e.privateStorageLocked(s.owner.catalog, candidate.ref.Address)
	if store == nil || store != candidate.storage {
		return rowVersion{}, false, ErrCatalogReplaced
	}
	rows := store.versions
	if candidate.own {
		rows = s.ownWrites[candidate.ref.Address]
	}
	row, _, found := rowByID(rows, candidate.row, candidate.ordinal)
	if !found {
		return rowVersion{}, false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := s.repeatableConflict(row, store.ends); err != nil {
		return rowVersion{}, false, err
	}
	return row, liveVersion(row, e.commitSeq, store.ends, s.ownDeletes[candidate.ref.Address]), nil
}

// The nil selection path remains allocation-free for undeleted prefixes.
func captureQuerySource(ctx context.Context, view *VisibilityView, ref ObjectRef) (*querySource, error) {
	source, err := captureQueryPrefix(view, ref)
	if err != nil {
		return nil, err
	}
	data := source.view.read()
	return selectQuerySource(ctx, source, data.frontierAt(ref.Address), data.ends[ref.Address], data.deleted[ref.Address])
}

func captureQueryPrefix(view *VisibilityView, ref ObjectRef) (*querySource, error) {
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
	return &querySource{view: pin, ref: ref, committed: data.committed[ref.Address], own: data.own[ref.Address]}, nil
}

// Query and catalog replacement share one physical-to-live head selection.
func selectQuerySource(ctx context.Context, source *querySource, frontier CommitSeq, ends *deletionRoot, own *privateDeletionRoot) (*querySource, error) {
	replacements, single := 0, 0
	for i, row := range source.own {
		if row.base != 0 {
			replacements++
			single = i
		}
	}
	if ends == nil && own == nil && replacements == 0 {
		return source, nil
	}
	total := len(source.committed) + len(source.own)
	var heads map[RowID]int
	if replacements > 1 {
		heads = make(map[RowID]int, replacements)
		for i, row := range source.own {
			if row.base != 0 {
				heads[row.row] = len(source.committed) + i
			}
		}
	}
	include := func(raw, ordinal int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		live := liveVersion(source.versionAt(raw), frontier, ends, own)
		if source.visible == nil && (!live || raw != ordinal) {
			source.visible = make([]int, ordinal, total)
			for j := range ordinal {
				source.visible[j] = j
			}
		}
		if live && source.visible != nil {
			source.visible = append(source.visible, raw)
		}
		return nil
	}
	for i, row := range source.committed {
		raw := i
		if replacements == 1 && row.row == source.own[single].row {
			raw = len(source.committed) + single
		}
		if head, ok := heads[row.row]; ok {
			raw = head
		}
		if err := include(raw, i); err != nil {
			source.close()
			return nil, err
		}
	}
	for i, row := range source.own {
		if row.base != 0 {
			continue
		}
		raw := len(source.committed) + i
		if err := include(raw, raw); err != nil {
			source.close()
			return nil, err
		}
	}
	return source, nil
}
