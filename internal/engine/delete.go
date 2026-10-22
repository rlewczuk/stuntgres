package engine

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

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
	store := e.privateStorageLocked(s.owner.Catalog, candidate.Ref.Address)
	if store == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.Own {
		rows = store.Versions
	}
	original, slot, found := storage.RowByID(rows, candidate.Row, candidate.Ordinal)
	relation, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(candidate.Ref.Address)
	if !found || !ok || len(original.Values) != len(relation.Columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !visibility.LiveVersion(original, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[candidate.Ref.Address]) {
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

func executeDelete(s *StatementScope, b analyze.Binding, view *visibility.View, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
	source, err := s.Command.CaptureSource(s.op.Context(), view, b)
	if err != nil {
		if inputs != nil {
			inputs.Close()
		}
		view.Release()
		return Result{}, err
	}
	defer source.Close()
	outcome, err := ExecuteDelete(s.writeHost(), DeleteInput{Binding: b, Source: source, Inputs: inputs, Parameters: parameters})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}
