package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type deleteCandidate struct {
	ref     ids.ObjectRef
	storage *storage.Relation
	row     ids.RowID
	version ids.VersionID
	base    ids.VersionID
	ordinal int
	own     bool
}
type privateDeletionRoot struct{ rows map[ids.RowID]deleteCandidate }

// A private replacement and an older retained snapshot may share RowID without
// sharing lineage. End only the deleted version and its actual replacement base.
func liveVersion(v storage.RowVersion, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *privateDeletionRoot) bool {
	if own != nil {
		if deleted, ok := own.rows[v.Row]; ok && (v.Version == deleted.version || deleted.base != 0 && v.Version == deleted.base) {
			return false
		}
	}
	if v.Born == 0 {
		return true
	}
	if v.Born > frontier {
		return false
	}
	if ends != nil {
		if end, deleted := ends.Rows[v.Row]; deleted && end.Version == v.Version && end.End <= frontier {
			return false
		}
	}
	return true
}

func mergeDeletions(previous, staged map[ids.ObjectAddress]*privateDeletionRoot) map[ids.ObjectAddress]*privateDeletionRoot {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ids.ObjectAddress]*privateDeletionRoot, len(previous)+len(staged))
	for a, root := range previous {
		next[a] = root
	}
	for a, root := range staged {
		old := previous[a]
		if old == nil {
			next[a] = root
			continue
		}
		rows := make(map[ids.RowID]deleteCandidate, len(old.rows)+len(root.rows))
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
	if s.ticket == nil || s.ticket.Ref() != s.txRef || txOwnerOf(s.txRef) != s.owner || s.txRef.Session() != s.op.session.token || candidate.storage == nil || candidate.storage != s.mutationStorage(candidate.ref) || candidate.row == 0 || candidate.version == 0 || candidate.ordinal < 0 {
		return errors.New("stuntgres: invalid row acquisition authority")
	}
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: candidate.ref, Row: candidate.row}, lock.ForUpdate, policy)
	if err != nil {
		return err
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		if err = request.Wait(); err != nil {
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
	if candidate.ref.Domain() != e.catalog.Domain || candidate.ref.Epoch != e.catalog.state.Epoch() || candidate.storage != e.privateStorageLocked(s.owner.catalog, candidate.ref.Address) {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Delete transfers only the original identity, never a value-based substitute.
// A peer committed deletion is a successful skip after arbitration.
func (w *WriteStage) Delete(candidate deleteCandidate) (deleted bool, err error) {
	if w == nil || w.self != w || w.scope == nil {
		return false, errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	if err := s.active(); err != nil {
		return false, err
	}
	if !s.op.session.Engine().locks.Holds(s.ticket, lock.Resource{ObjectRef: candidate.ref, Row: candidate.row}, lock.ForUpdate) {
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
	if candidate.ref.Domain() != e.catalog.Domain || candidate.ref.Address.SubID != 0 || candidate.row == 0 || candidate.version == 0 {
		return false, errors.New("stuntgres: invalid deletion identity")
	}
	if candidate.ref.Epoch != e.catalog.state.Epoch() || candidate.ref.Epoch != s.txRef.Epoch() || candidate.storage == nil || candidate.storage != e.privateStorageLocked(s.owner.catalog, candidate.ref.Address) {
		return false, ids.ErrCatalogReplaced
	}
	if s.view == nil {
		return false, errors.New("stuntgres: deletion requires its candidate view")
	}
	data := s.view.read()
	if candidate.storage != s.mutationStorage(candidate.ref) || candidate.storage != data.generationAt(candidate.ref.Address) {
		return false, errors.New("stuntgres: deletion is outside its candidate shape")
	}
	captured := data.committed[candidate.ref.Address]
	if candidate.own {
		captured = data.own[candidate.ref.Address]
	}
	if candidate.ordinal < 0 || candidate.ordinal >= len(captured) || captured[candidate.ordinal].Row != candidate.row || captured[candidate.ordinal].Version != candidate.version {
		return false, errors.New("stuntgres: deletion is outside its candidate view")
	}
	rows := s.ownWrites[candidate.ref.Address]
	storage := e.privateStorageLocked(s.owner.catalog, candidate.ref.Address)
	if storage == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.own {
		rows = storage.Versions
	}
	original, slot, found := rowByID(rows, candidate.row, candidate.ordinal)
	relation, ok := e.privateCatalogLocked(s.owner.catalog).Relation(candidate.ref.Address)
	if !found || !ok || len(original.Values) != len(relation.Columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !liveVersion(original, e.visibility.commitSeq, storage.Ends, s.ownDeletes[candidate.ref.Address]) {
		return false, nil
	}
	candidate.version, candidate.ordinal = original.Version, slot
	if candidate.own {
		candidate.base = original.Base
	}
	root := w.deletes[candidate.ref.Address]
	if root != nil {
		if _, duplicate := root.rows[candidate.row]; duplicate {
			return false, errors.New("stuntgres: duplicate staged deletion")
		}
	}
	if err := w.stageDeletionLocked(e, candidate, original); err != nil {
		return false, err
	}
	return true, nil
}

// Both named-target and separately admitted RI deletion use the same roots,
// SSI evidence and exclusion projection after validating their own authority.
func (w *WriteStage) stageDeletionLocked(e *Engine, candidate deleteCandidate, original storage.RowVersion) error {
	s := w.scope
	event := [1]serializableWrite{{row: candidate.row, version: candidate.version, base: original.Version, baseBorn: original.Born, creator: e.visibility.serializable.creators[original.Version], deletion: true}}
	if err := e.serializableWriteLocked(s, candidate.ref, event[:]); err != nil {
		return err
	}
	if w.deletes == nil {
		w.deletes = make(map[ids.ObjectAddress]*privateDeletionRoot)
	}
	root := w.deletes[candidate.ref.Address]
	if root == nil {
		root = &privateDeletionRoot{rows: make(map[ids.RowID]deleteCandidate)}
		w.deletes[candidate.ref.Address] = root
	}
	root.rows[candidate.row] = candidate
	s.projectExclusionDeletionLocked(e, candidate)
	s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: candidate.ref, Row: original.Row}, original.Version}, Mode: lock.MultiUpdateAction})
	return nil
}

func (s *StatementScope) executeDelete(b binding, view *VisibilityView, inputs *querySource, parameters []sqltype.Value) (Result, error) {
	source, err := s.captureMutationSource(s.op.Context(), view, b)
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
	if b.storage == nil || b.storage != s.lockedStorage || b.storage != source.view.read().generationAt(b.ref.Address) {
		if inputs != nil {
			inputs.close()
		}
		return Result{}, ids.ErrCatalogReplaced
	}
	var positioned mutationRowIdentity
	if b.currentOf != "" {
		var err error
		positioned, err = s.positionedMutationRow(b)
		if err != nil {
			if inputs != nil {
				inputs.close()
			}
			return Result{}, err
		}
	}
	ctx := s.op.Context()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: parameters, statementScope: s, Input: s.input, Output: s.output}
	if inputs != nil {
		if err := selectQuerySources(ctx, inputs); err != nil {
			return Result{}, err
		}
		if b.viewWrite != nil || b.ruleTemplate {
			inputs = freezeViewWriteSources(s, inputs, b.querySources)
		}
		defer inputs.close()
		state := &queryState{source: inputs, parameters: parameters, input: s.input, output: s.output, locking: inputs.locks != nil}
		defer state.releaseMemberships()
		inputs.scope = s
		evaluation.query = state
	}
	if b.deleteUsing != nil {
		return s.deleteUsingSource(b, source, &evaluation, positioned)
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
		physical := mutationPhysicalRow(source, i, b)
		version := physical.version
		if b.currentOf != "" && (positioned.ref != physical.leaf.ref || version.Row != positioned.row) {
			continue
		}
		evaluation.Row = physical.values(version)
		evaluation.physicalTable = physical.leaf.ref
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
		candidate := physical.deletion()
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
		if current.Version != candidate.version {
			if err := s.physicalRowEvent(candidate.ref, current, lock.MultiUpdateLock, 0); err != nil {
				return Result{}, err
			}
		}
		if current.Version != candidate.version && b.predicate != nil {
			evaluation.Row = physical.values(current)
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
		if err := s.claimUniqueRow(candidate.ref, current); err != nil {
			return Result{}, err
		}
		deleted, err := s.Writes().Delete(candidate)
		if err != nil {
			return Result{}, err
		}
		if deleted {
			count++
			if rows != nil {
				evaluation.Row = physical.values(current)
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
func (s *StatementScope) latestDeleteRow(candidate deleteCandidate) (storage.RowVersion, bool, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, false, err
	}
	store := e.privateStorageLocked(s.owner.catalog, candidate.ref.Address)
	if store == nil || store != candidate.storage {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	rows := store.Versions
	if candidate.own {
		rows = s.ownWrites[candidate.ref.Address]
	}
	row, _, found := rowByID(rows, candidate.row, candidate.ordinal)
	if !found {
		return storage.RowVersion{}, false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return storage.RowVersion{}, false, err
	}
	return row, liveVersion(row, e.visibility.commitSeq, store.Ends, s.ownDeletes[candidate.ref.Address]), nil
}

// The nil selection path remains allocation-free for undeleted prefixes.
func captureQuerySource(ctx context.Context, view *VisibilityView, ref ids.ObjectRef) (*querySource, error) {
	source, err := captureQueryPrefix(view, ref)
	if err != nil {
		return nil, err
	}
	data := source.view.read()
	return selectQuerySource(ctx, source, data.frontierAt(ref.Address), data.ends[ref.Address], data.deleted[ref.Address])
}

func captureQueryPrefix(view *VisibilityView, ref ids.ObjectRef) (*querySource, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	data := pin.read()
	if data.catalog.Owner() == nil {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility catalog")
	}
	if _, ok := data.committed[ref.Address]; !ok || ref.Domain() != data.catalog.Owner().Domain || ref.Epoch != data.catalog.Epoch() {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility target")
	}
	return &querySource{view: pin, ref: ref, committed: data.committed[ref.Address], own: data.own[ref.Address], needsBirthFilter: data.needsBirthFilterAt(ref.Address)}, nil
}

// Query and catalog replacement share one physical-to-live head selection.
func selectQuerySource(ctx context.Context, source *querySource, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *privateDeletionRoot) (*querySource, error) {
	if source.rlsDenied {
		return source, ctx.Err()
	}
	replacements, single := 0, 0
	for i, row := range source.own {
		if row.Base != 0 {
			replacements++
			single = i
		}
	}
	if ends == nil && own == nil && replacements == 0 && !source.needsBirthFilter {
		return source, nil
	}
	total := len(source.committed) + len(source.own)
	var heads map[ids.VersionID]int
	singleSubstituted := false
	if replacements > 1 {
		heads = make(map[ids.VersionID]int, replacements)
		for i, row := range source.own {
			if row.Base != 0 {
				heads[row.Base] = len(source.committed) + i
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
		if replacements == 1 && row.Row == source.own[single].Row && row.Version == source.own[single].Base {
			raw = len(source.committed) + single
			singleSubstituted = true
		}
		if head, ok := heads[row.Version]; ok && source.versionAt(head).Row == row.Row {
			raw = head
			delete(heads, row.Version)
		}
		if err := include(raw, i); err != nil {
			source.close()
			return nil, err
		}
	}
	for i, row := range source.own {
		if row.Base != 0 && ((replacements == 1 && singleSubstituted) || (replacements > 1 && heads[row.Base] == 0)) {
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
