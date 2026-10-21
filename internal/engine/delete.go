package engine

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func mergeDeletions(previous, staged map[ids.ObjectAddress]*visibility.PrivateDeletes) map[ids.ObjectAddress]*visibility.PrivateDeletes {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ids.ObjectAddress]*visibility.PrivateDeletes, len(previous)+len(staged))
	for a, root := range previous {
		next[a] = root
	}
	for a, root := range staged {
		old := previous[a]
		if old == nil {
			next[a] = root
			continue
		}
		rows := make(map[ids.RowID]visibility.DeleteCandidate, len(old.Rows)+len(root.Rows))
		for id, candidate := range old.Rows {
			rows[id] = candidate
		}
		for id, candidate := range root.Rows {
			rows[id] = candidate
		}
		next[a] = &visibility.PrivateDeletes{Rows: rows}
	}
	return next
}

func (s *StatementScope) acquireRow(candidate visibility.DeleteCandidate) error {
	if err := s.active(); err != nil {
		return err
	}
	if s.ticket == nil || s.ticket.Ref() != s.txRef || session.TxOwnerOf(s.txRef) != s.owner || s.txRef.Session() != s.op.Session.SessionToken || candidate.Storage == nil || candidate.Storage != s.mutationStorage(candidate.Ref) || candidate.Row == 0 || candidate.Version == 0 || candidate.Ordinal < 0 {
		return errors.New("stuntgres: invalid row acquisition authority")
	}
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: candidate.Ref, Row: candidate.Row}, lock.ForUpdate, policy)
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
	if candidate.Ref.Domain() != e.catalog.Domain || candidate.Ref.Epoch != e.catalog.state.Epoch() || candidate.Storage != e.privateStorageLocked(s.owner.Catalog, candidate.Ref.Address) {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Delete transfers only the original identity, never a value-based substitute.
// A peer committed deletion is a successful skip after arbitration.
func (w *WriteStage) Delete(candidate visibility.DeleteCandidate) (deleted bool, err error) {
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
	if !s.engine().locks.Holds(s.ticket, lock.Resource{ObjectRef: candidate.Ref, Row: candidate.Row}, lock.ForUpdate) {
		return false, errors.New("stuntgres: deletion requires its row grant")
	}
	s.owner.Mu.Lock()
	defer s.owner.Mu.Unlock()
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted || s.owner.Ref != s.txRef {
		return false, errors.New("stuntgres: inactive write stage")
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if candidate.Ref.Domain() != e.catalog.Domain || candidate.Ref.Address.SubID != 0 || candidate.Row == 0 || candidate.Version == 0 {
		return false, errors.New("stuntgres: invalid deletion identity")
	}
	if candidate.Ref.Epoch != e.catalog.state.Epoch() || candidate.Ref.Epoch != s.txRef.Epoch() || candidate.Storage == nil || candidate.Storage != e.privateStorageLocked(s.owner.Catalog, candidate.Ref.Address) {
		return false, ids.ErrCatalogReplaced
	}
	if s.view == nil {
		return false, errors.New("stuntgres: deletion requires its candidate view")
	}
	data := s.view.Data()
	if candidate.Storage != s.mutationStorage(candidate.Ref) || candidate.Storage != data.GenerationAt(candidate.Ref.Address) {
		return false, errors.New("stuntgres: deletion is outside its candidate shape")
	}
	captured := data.Committed[candidate.Ref.Address]
	if candidate.Own {
		captured = data.Own[candidate.Ref.Address]
	}
	if candidate.Ordinal < 0 || candidate.Ordinal >= len(captured) || captured[candidate.Ordinal].Row != candidate.Row || captured[candidate.Ordinal].Version != candidate.Version {
		return false, errors.New("stuntgres: deletion is outside its candidate view")
	}
	rows := s.ownWrites[candidate.Ref.Address]
	storage := e.privateStorageLocked(s.owner.Catalog, candidate.Ref.Address)
	if storage == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.Own {
		rows = storage.Versions
	}
	original, slot, found := rowByID(rows, candidate.Row, candidate.Ordinal)
	relation, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(candidate.Ref.Address)
	if !found || !ok || len(original.Values) != len(relation.Columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !visibility.LiveVersion(original, e.visibility.CommitSeq(), storage.Ends, s.ownDeletes[candidate.Ref.Address]) {
		return false, nil
	}
	candidate.Version, candidate.Ordinal = original.Version, slot
	if candidate.Own {
		candidate.Base = original.Base
	}
	root := w.deletes[candidate.Ref.Address]
	if root != nil {
		if _, duplicate := root.Rows[candidate.Row]; duplicate {
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
func (w *WriteStage) stageDeletionLocked(e *Engine, candidate visibility.DeleteCandidate, original storage.RowVersion) error {
	s := w.scope
	event := [1]visibility.SerializableWrite{{Row: candidate.Row, Version: candidate.Version, Base: original.Version, BaseBorn: original.Born, Creator: e.visibility.SerializableCreatorLocked(original.Version), Deletion: true}}
	if err := e.serializableWriteLocked(s, candidate.Ref, event[:]); err != nil {
		return err
	}
	if w.deletes == nil {
		w.deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
	}
	root := w.deletes[candidate.Ref.Address]
	if root == nil {
		root = &visibility.PrivateDeletes{Rows: make(map[ids.RowID]visibility.DeleteCandidate)}
		w.deletes[candidate.Ref.Address] = root
	}
	root.Rows[candidate.Row] = candidate
	s.projectExclusionDeletionLocked(e, candidate)
	s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: candidate.Ref, Row: original.Row}, original.Version}, Mode: lock.MultiUpdateAction})
	return nil
}

func (s *StatementScope) executeDelete(b analyze.Binding, view *visibility.View, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
	source, err := s.captureMutationSource(s.op.Context(), view, b)
	if err != nil {
		if inputs != nil {
			inputs.Close()
		}
		view.Release()
		return Result{}, err
	}
	defer source.Close()
	return s.deleteSource(b, source, inputs, parameters)
}

func (s *StatementScope) deleteSource(b analyze.Binding, source, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
	if b.Storage == nil || b.Storage != s.lockedStorage || b.Storage != source.View.Data().GenerationAt(b.Ref.Address) {
		if inputs != nil {
			inputs.Close()
		}
		return Result{}, ids.ErrCatalogReplaced
	}
	var positioned mutationRowIdentity
	if b.CurrentOf != "" {
		var err error
		positioned, err = s.positionedMutationRow(b)
		if err != nil {
			if inputs != nil {
				inputs.Close()
			}
			return Result{}, err
		}
	}
	ctx := s.op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: parameters, Host: hostOf(s), Input: s.input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if inputs != nil {
		if err := exec.SelectQuerySources(ctx, inputs); err != nil {
			return Result{}, err
		}
		if b.ViewWrite != nil || b.Rule.Template {
			inputs = freezeViewWriteSources(s, inputs, b.QuerySources)
		}
		defer inputs.Close()
		state := &exec.State{Source: inputs, Parameters: parameters, Input: s.input, Output: s.output, Locking: inputs.Locks != nil}
		defer state.ReleaseMemberships()
		inputs.Host = hostOf(s)
		evaluation.Query = state
	}
	if b.Delete.Using != nil {
		return s.deleteUsingSource(b, source, &evaluation, positioned)
	}
	var count int64
	var rows *exec.FinalRows
	keep := false
	if b.Returning != nil {
		rows = exec.NewFinalRows(b.Returning.Schema, 0)
		rows.Output = s.output
		defer func() {
			if !keep {
				rows.Close()
			}
		}()
	}
	for i := range source.Count() {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		physical := mutationPhysicalRow(source, i, b)
		version := physical.Version
		if b.CurrentOf != "" && (positioned.ref != physical.Leaf.Ref || version.Row != positioned.row) {
			continue
		}
		evaluation.Row = physical.Values(version)
		evaluation.PhysicalTable = physical.Leaf.Ref
		if b.Predicate != nil {
			value, err := b.Predicate.Eval(&evaluation)
			if err != nil {
				return Result{}, err
			}
			truth, err := plan.TruthOf(value)
			if err != nil {
				return Result{}, err
			}
			if truth != plan.True {
				continue
			}
		}
		candidate := physical.Deletion()
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
		if current.Version != candidate.Version {
			if err := s.physicalRowEvent(candidate.Ref, current, lock.MultiUpdateLock, 0); err != nil {
				return Result{}, err
			}
		}
		if current.Version != candidate.Version && b.Predicate != nil {
			evaluation.Row = physical.Values(current)
			value, err := b.Predicate.Eval(&evaluation)
			if err != nil {
				return Result{}, err
			}
			truth, err := plan.TruthOf(value)
			if err != nil {
				return Result{}, err
			}
			if truth != plan.True {
				continue
			}
		}
		if err := s.claimUniqueRow(candidate.Ref, current); err != nil {
			return Result{}, err
		}
		deleted, err := s.Writes().Delete(candidate)
		if err != nil {
			return Result{}, err
		}
		if deleted {
			count++
			if rows != nil {
				evaluation.Row = physical.Values(current)
				for output, expr := range b.Returning.Expressions {
					value, err := expr.Eval(&evaluation)
					if err != nil {
						return Result{}, err
					}
					if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.Columns[output].Usage.Type {
						return Result{}, errors.New("stuntgres: invalid finalized DELETE value")
					}
					rows.Values = append(rows.Values, value)
				}
				rows.Count++
			}
		}
	}
	if rows != nil && b.Returning.RegclassOutput && count != 0 {
		if err := s.engine().captureRegclassOutput(s.op, rows); err != nil {
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
func (s *StatementScope) latestDeleteRow(candidate visibility.DeleteCandidate) (storage.RowVersion, bool, error) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, false, err
	}
	store := e.privateStorageLocked(s.owner.Catalog, candidate.Ref.Address)
	if store == nil || store != candidate.Storage {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	rows := store.Versions
	if candidate.Own {
		rows = s.ownWrites[candidate.Ref.Address]
	}
	row, _, found := rowByID(rows, candidate.Row, candidate.Ordinal)
	if !found {
		return storage.RowVersion{}, false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return storage.RowVersion{}, false, err
	}
	return row, visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[candidate.Ref.Address]), nil
}
