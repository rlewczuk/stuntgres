package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
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
	if s.ticket == nil || s.ticket.Ref() != s.txRef || txOwnerOf(s.txRef) != s.owner || s.txRef.Session() != s.op.session.token || candidate.Storage == nil || candidate.Storage != s.mutationStorage(candidate.Ref) || candidate.Row == 0 || candidate.Version == 0 || candidate.Ordinal < 0 {
		return errors.New("stuntgres: invalid row acquisition authority")
	}
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
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
	if candidate.Ref.Domain() != e.catalog.Domain || candidate.Ref.Epoch != e.catalog.state.Epoch() || candidate.Storage != e.privateStorageLocked(s.owner.catalog, candidate.Ref.Address) {
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
	if !s.op.session.Engine().locks.Holds(s.ticket, lock.Resource{ObjectRef: candidate.Ref, Row: candidate.Row}, lock.ForUpdate) {
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
	if candidate.Ref.Domain() != e.catalog.Domain || candidate.Ref.Address.SubID != 0 || candidate.Row == 0 || candidate.Version == 0 {
		return false, errors.New("stuntgres: invalid deletion identity")
	}
	if candidate.Ref.Epoch != e.catalog.state.Epoch() || candidate.Ref.Epoch != s.txRef.Epoch() || candidate.Storage == nil || candidate.Storage != e.privateStorageLocked(s.owner.catalog, candidate.Ref.Address) {
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
	storage := e.privateStorageLocked(s.owner.catalog, candidate.Ref.Address)
	if storage == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.Own {
		rows = storage.Versions
	}
	original, slot, found := rowByID(rows, candidate.Row, candidate.Ordinal)
	relation, ok := e.privateCatalogLocked(s.owner.catalog).Relation(candidate.Ref.Address)
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

func (s *StatementScope) executeDelete(b binding, view *visibility.View, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
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

func (s *StatementScope) deleteSource(b binding, source, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
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
func (s *StatementScope) latestDeleteRow(candidate visibility.DeleteCandidate) (storage.RowVersion, bool, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, false, err
	}
	store := e.privateStorageLocked(s.owner.catalog, candidate.Ref.Address)
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

// The nil selection path remains allocation-free for undeleted prefixes.
func captureQuerySource(ctx context.Context, view *visibility.View, ref ids.ObjectRef) (*exec.Source, error) {
	source, err := captureQueryPrefix(view, ref)
	if err != nil {
		return nil, err
	}
	data := source.View.Data()
	return exec.SelectQuerySource(ctx, source, data.FrontierAt(ref.Address), data.Ends[ref.Address], data.Deleted[ref.Address])
}

func captureQueryPrefix(view *visibility.View, ref ids.ObjectRef) (*exec.Source, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	data := pin.Data()
	if data.Catalog.Owner() == nil {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility catalog")
	}
	if _, ok := data.Committed[ref.Address]; !ok || ref.Domain() != data.Catalog.Owner().Domain || ref.Epoch != data.Catalog.Epoch() {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility target")
	}
	return &exec.Source{View: pin, Ref: ref, Committed: data.Committed[ref.Address], Own: data.Own[ref.Address], NeedsBirthFilter: data.NeedsBirthFilterAt(ref.Address)}, nil
}
