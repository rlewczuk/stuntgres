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
	"stuntgres/internal/engine/stored"
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
	return s.acquireUpdateRowMode(candidate.Ref, storage.RowVersion{Row: candidate.Row, Version: candidate.Version}, lock.ForUpdate)
}

// Delete transfers only the original identity, never a value-based substitute.
// A peer committed deletion is a successful skip after arbitration.
func (w *WriteStage) Delete(candidate visibility.DeleteCandidate, expected storage.RowVersion) (deleted bool, err error) {
	if w == nil || w.self != w || w.scope == nil {
		return false, errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	var afterOld []sqltype.Value
	defer func() {
		if err == nil && deleted && afterOld != nil {
			err = s.WriteHost().AfterRow(dml.BeforeRow{Relation: candidate.Ref, Event: dml.BeforeDelete, Old: afterOld})
		}
	}()
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
	original, hint, err := s.lockDeletionStage(candidate, expected)
	if err != nil {
		return false, err
	}
	e := s.Engine()
	defer s.Owner.Mu.Unlock()
	defer e.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted || s.Owner.Ref != s.TxRef {
		return false, errors.New("stuntgres: inactive write stage")
	}
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
	capturedHere := candidate.Ordinal >= 0 && candidate.Ordinal < len(captured) && captured[candidate.Ordinal].Row == candidate.Row && captured[candidate.Ordinal].Version == candidate.Version
	if !capturedHere && data.Headers[candidate.Ref.Address] != nil {
		_, _, inPrefix := storage.RowByVersion(captured, candidate.Row, candidate.Version, -1)
		if !inPrefix {
			_, _, capturedHere = storage.RowByVersion(data.Headers[candidate.Ref.Address].Additional, candidate.Row, candidate.Version, -1)
		}
	}
	if !capturedHere {
		return false, errors.New("stuntgres: deletion is outside its candidate view")
	}
	rows := s.OwnWrites[candidate.Ref.Address]
	store := e.PrivateStorageLocked(s.PendingCatalog(), candidate.Ref.Address)
	if store == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	candidate.Own = original.Born == 0 && !original.ForcedFrozen
	var slot int
	var found bool
	if candidate.Own {
		_, slot, found = storage.RowByVersion(rows, original.Row, original.Version, hint)
		if !found {
			_, slot, found = storage.RowByVersion(w.Rows[candidate.Ref.Address], original.Row, original.Version, hint)
		}
	} else {
		_, slot, found = store.PhysicalVersion(original.Version, hint)
	}
	relation, ok := e.PrivateCatalogLocked(s.PendingCatalog()).Relation(candidate.Ref.Address)
	if !found || !ok || len(original.Values) != len(relation.Columns) {
		return false, errors.New("stuntgres: invalid deletion predecessor")
	}
	candidate.Version, candidate.Ordinal = original.Version, slot
	candidate.RepairStamp = original.RepairStamp
	candidate.BaseRepairStamp = original.BaseHeaderStamp()
	if candidate.Own {
		candidate.Base = original.Base
	} else {
		candidate.Base = 0
		candidate.BaseRepairStamp = 0
	}
	root := w.Deletes[candidate.Ref.Address]
	if root != nil {
		if existing, duplicate := root.Lookup(candidate.Row, candidate.Version); duplicate && existing.Version == candidate.Version {
			return false, errors.New("stuntgres: duplicate staged deletion")
		}
	}
	if err := w.stageDeletionLocked(e, candidate, original); err != nil {
		return false, err
	}
	if s.needsAfterRow(candidate.Ref, relation, stored.TriggerDelete) {
		afterOld = original.Values
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
	statsWriteStart := s.beginDatabaseWrite()
	if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
		return err
	}
	candidate.EndFullXID = e.Visibility.FullXIDLocked(s.TxRef)
	physical := original
	physical.RepairStamp = candidate.RepairStamp
	candidate.Storage.Headers = storage.EndHeapHeaders(candidate.Storage.Headers, []storage.RowVersion{physical}, candidate.EndFullXID)
	if w.Deletes == nil {
		w.Deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
	}
	root := w.Deletes[candidate.Ref.Address]
	if root == nil {
		root = &visibility.PrivateDeletes{Rows: make(map[ids.RowID]visibility.DeleteCandidate)}
		w.Deletes[candidate.Ref.Address] = root
	}
	root.Set(candidate)
	if s.foreignEffectRelevant(candidate.Ref, e.PrivateCatalogLocked(s.PendingCatalog())) {
		s.recordForeignEffect(candidate.Ref, original, storage.RowVersion{})
	}
	s.captureLogicalRow(candidate.Ref, original, storage.RowVersion{})
	s.projectExclusionDeletionLocked(e, candidate)
	s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{Resource: s.admittedTuple(candidate.Ref, original), Version: original.Version}, Mode: lock.MultiUpdateAction})
	e.initializeTemporaryBuffersLocked(s, candidate.Ref)
	s.recordStatsTuple(candidate.Ref, 0, 0, 1, statsWriteStart.counts)
	s.finishDatabaseWrite(statsWriteStart)
	s.recordAutoExplainWrite(1)
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
	outcome, err := dml.ExecuteDelete(s.WriteHost(), dml.DeleteInput{Context: s.ActivateRuntime(s.executionContext()), Binding: b, Source: source, Inputs: inputs, Parameters: parameters})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}
