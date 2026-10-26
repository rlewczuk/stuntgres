package core

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) acquireRow(candidate visibility.DeleteCandidate) error {
	if err := s.Active(); err != nil {
		return err
	}
	if s.LockTicket == nil || s.LockTicket.Ref() != s.TxRef || session.TxOwnerOf(s.TxRef) != s.Owner || s.TxRef.Session() != s.Op.Session.SessionToken || candidate.Storage == nil || candidate.Storage != s.mutationStorage(candidate.Ref) || candidate.Row == 0 || candidate.Version == 0 || candidate.Ordinal < 0 {
		return errors.New("stuntgres: invalid row acquisition authority")
	}
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: candidate.Ref, Row: candidate.Row}, lock.ForUpdate, policy)
	if err != nil {
		return err
	}
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		if err = request.Wait(); err != nil {
			return err
		}
		if s.implicit {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err = s.ValidateLockedRelation(e); err != nil {
		return err
	}
	if candidate.Ref.Domain() != e.Catalog.Domain || candidate.Ref.Epoch != e.Catalog.State.Epoch() || candidate.Storage != e.PrivateStorageLocked(s.PendingCatalog(), candidate.Ref.Address) {
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
	if err := s.Active(); err != nil {
		return false, err
	}
	if !s.Engine().Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: candidate.Ref, Row: candidate.Row}, lock.ForUpdate) {
		return false, errors.New("stuntgres: deletion requires its row grant")
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted || s.Owner.Ref != s.TxRef {
		return false, errors.New("stuntgres: inactive write stage")
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if candidate.Ref.Domain() != e.Catalog.Domain || candidate.Ref.Address.SubID != 0 || candidate.Row == 0 || candidate.Version == 0 {
		return false, errors.New("stuntgres: invalid deletion identity")
	}
	if candidate.Ref.Epoch != e.Catalog.State.Epoch() || candidate.Ref.Epoch != s.TxRef.Epoch() || candidate.Storage == nil || candidate.Storage != e.PrivateStorageLocked(s.PendingCatalog(), candidate.Ref.Address) {
		return false, ids.ErrCatalogReplaced
	}
	if s.View == nil {
		return false, errors.New("stuntgres: deletion requires its candidate view")
	}
	data := s.View.Data()
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
	rows := s.OwnWrites[candidate.Ref.Address]
	store := e.PrivateStorageLocked(s.PendingCatalog(), candidate.Ref.Address)
	if store == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	if !candidate.Own {
		rows = store.Versions
	}
	original, slot, found := storage.RowByID(rows, candidate.Row, candidate.Ordinal)
	relation, ok := e.PrivateCatalogLocked(s.PendingCatalog()).Relation(candidate.Ref.Address)
	if !found || !ok || len(original.Values) != len(relation.Columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	if !visibility.LiveVersion(original, e.Visibility.CommitSeq(), store.Ends, s.OwnDeletes[candidate.Ref.Address]) {
		return false, nil
	}
	candidate.Version, candidate.Ordinal = original.Version, slot
	if candidate.Own {
		candidate.Base = original.Base
	}
	root := w.Deletes[candidate.Ref.Address]
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
	event := [1]visibility.SerializableWrite{{Row: candidate.Row, Version: candidate.Version, Base: original.Version, BaseBorn: original.Born, Creator: e.Visibility.SerializableCreatorLocked(original.Version), Deletion: true}}
	if err := e.SerializableWriteLocked(s, candidate.Ref, event[:]); err != nil {
		return err
	}
	if w.Deletes == nil {
		w.Deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
	}
	root := w.Deletes[candidate.Ref.Address]
	if root == nil {
		root = &visibility.PrivateDeletes{Rows: make(map[ids.RowID]visibility.DeleteCandidate)}
		w.Deletes[candidate.Ref.Address] = root
	}
	root.Rows[candidate.Row] = candidate
	if s.foreignEffectRelevant(candidate.Ref, e.PrivateCatalogLocked(s.PendingCatalog())) {
		s.recordForeignEffect(candidate.Ref, original, storage.RowVersion{})
	}
	s.projectExclusionDeletionLocked(e, candidate)
	s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: candidate.Ref, Row: original.Row}, original.Version}, Mode: lock.MultiUpdateAction})
	e.initializeTemporaryBuffersLocked(s, candidate.Ref)
	return nil
}

func executeDelete(s *StatementScope, b analyze.Binding, view *visibility.View, inputs *exec.Source, parameters []sqltype.Value) (Result, error) {
	source, err := s.Command.CaptureSource(s.Op.Context(), view, b)
	if err != nil {
		if inputs != nil {
			inputs.Close()
		}
		view.Release()
		return Result{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	e.initializeTemporarySourceLocked(s, source)
	e.Mu.Unlock()
	defer source.Close()
	outcome, err := dml.ExecuteDelete(s.WriteHost(), dml.DeleteInput{Binding: b, Source: source, Inputs: inputs, Parameters: parameters})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}
