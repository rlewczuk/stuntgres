package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func captureSavepoint(t *session.TransactionOwner, name string) session.SavepointMark {
	e := EngineOf(t.Session)
	e.Mu.Lock()
	projection := e.Exclusions.OwnLocked(t.Ticket)
	e.Mu.Unlock()
	t.Session.Mu.Lock()
	identity := t.Session.Authorization.State
	t.Session.Mu.Unlock()
	return t.CaptureSavepoint(session.SavepointMark{Name: name, Settings: t.Session.Settings.Savepoint(), Locks: e.Locks.Checkpoint(t.Ticket), Cursors: PayloadOf(t.Session).Cursors.ordinal, LargeObjects: ownerExtensionOrZero(t).largeObjects.ordinal, Exclusion: index.ExclusionMarkSlotOf(ownerExtensionOrZero(t).constraintModes, projection), Identity: identity})
}

// restoreSavepoint rolls the owner back to mark outside ROLLBACK TO: failed
// statements and PL/pgSQL exception blocks. t.Mu is held. Enrollment follows
// the characteristic rules, which only re-enroll before the first snapshot.
func restoreSavepoint(t *session.TransactionOwner, mark session.SavepointMark, settings session.SettingsMark) {
	e := EngineOf(t.Session)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	t.Session.Settings.Mu.Lock()
	t.Session.Mu.Lock()
	e.restoreSavepointLocked(t, mark, settings)
	t.Session.Mu.Unlock()
	t.Session.Settings.Mu.Unlock()
	if !t.SnapshotSet && (t.State == session.TxActive || t.State == session.TxImplicit) {
		e.enrollSerializableLocked(t.Ref, t.Isolation)
	}
	e.reconcileSerializableWritesLocked(t)
}

// restoreSavepointLocked restores everything but SSI from mark: cursors,
// protocol portals, the owner's state and settings, and exclusions. Engine.mu
// -> Settings.Mu -> Session.Mu are held, with t.Mu.
func (e *Engine) restoreSavepointLocked(t *session.TransactionOwner, mark session.SavepointMark, settings session.SettingsMark) {
	ownerExtensionOf(t).detachCursors(PayloadOf(t.Session).Cursors.rollback(mark.Cursors))
	ownerExtensionOf(t).largeObjects.rollback(mark.LargeObjects)
	retireProtocolPortalsAfter(t, mark.Cursors)
	t.RollbackToLocked(mark, settings)
	e.reconcileReplicationOriginsLocked(t)
	exclusion := index.ExclusionMarkOrZero(mark.Exclusion)
	ownerExtensionOf(t).constraintModes = exclusion.Modes
	e.Exclusions.RestoreLocked(t.Ticket, exclusion.Projection)
}

// Called owner-first. Preparation intentionally performs no name lookup.
func (s *StatementScope) gateSavepoint(control *parser.SavepointControl) error {
	t, e := s.Owner, s.Engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if s.message {
		if err := s.admitTransactionLocked(e); err != nil {
			return err
		}
	}
	if err := e.endPreflight(s.Op); err != nil {
		return err
	}
	if t.State == session.TxAborted && !session.AbortedControlAllowed(control) {
		return session.AbortedError()
	}
	return nil
}

func (s *StatementScope) executeSavepoint(control *parser.SavepointControl, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := CheckStatementSignature(*s.Expected, Description{}); err != nil {
			return Result{}, err
		}
	}
	t, e := s.Owner, s.Engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.State == session.TxIdle || t.State == session.TxImplicit {
		return Result{}, session.SavepointOutsideBlockError(control.Kind)
	}
	// A replaced incarnation cannot recover through a discarded savepoint.
	// Fence before inspecting marks or invoking foreign savepoint callbacks.
	e.Mu.Lock()
	s.Op.Session.Mu.Lock()
	err := PublicationOperationLocked(e, s.Op)
	if err == nil {
		err = t.Validate(t.Ref)
	}
	if err == nil && t.Ref.Epoch() != e.Catalog.State.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	s.Op.Session.Mu.Unlock()
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	position := -1
	var mark session.SavepointMark
	var settings session.SettingsMark
	var stack []session.SavepointMark
	if control.Kind == parser.CreateSavepoint {
		if t.SavepointSerial == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: foreign savepoint identity exhausted")
		}
		mark = captureSavepoint(t, control.Name.Text)
		// Reserve the stack before the final callback-free preflight.
		stack = slices.Grow(t.Savepoints, 1)
	} else {
		position = t.FindSavepoint(control.Name.Text)
		if position >= 0 {
			mark = t.Savepoints[position]
			if control.Kind == parser.RollbackSavepoint {
				settings = mark.Settings.Clone()
			}
		}
	}
	if control.Kind != parser.CreateSavepoint && position < 0 {
		return Result{}, SQLError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
	}
	kind := 0
	if control.Kind == parser.RollbackSavepoint {
		kind = 1
	}
	if control.Kind == parser.ReleaseSavepoint {
		kind = 2
	}
	s.foreignUnlocked()
	t.Mu.Unlock()
	hookErr := foreignSavepoint(t, s.Op.Context(), kind, mark.Serial)
	t.Mu.Lock()
	if hookErr != nil {
		return Result{}, hookErr
	}
	result, err := func() (Result, error) {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		t.Session.Settings.Mu.Lock()
		defer t.Session.Settings.Mu.Unlock()
		s.Op.Session.Mu.Lock()
		defer s.Op.Session.Mu.Unlock()
		if err := PublicationOperationLocked(e, s.Op); err != nil {
			return Result{}, err
		}
		if err := t.Validate(t.Ref); err != nil {
			return Result{}, err
		}
		if t.Ref.Epoch() != e.Catalog.State.Epoch() {
			return Result{}, ids.ErrCatalogReplaced
		}
		if control.Kind != parser.CreateSavepoint && position < 0 {
			return Result{}, SQLError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
		}
		if (t.IdentityCaptured || t.Session.Settings.RoleAction.Present) && (control.Kind == parser.CreateSavepoint || control.Kind == parser.RollbackSavepoint) {
			// Reserve failed-preflight restoration and remaining boundaries.
			if err := session.RoleRevisionHeadroom(t.Session.Authorization, uint64(len(t.Savepoints))+3); err != nil {
				return Result{}, err
			}
		}
		switch control.Kind {
		case parser.CreateSavepoint:
			t.Savepoints = append(stack, mark)
		case parser.RollbackSavepoint:
			e.restoreSavepointLocked(t, mark, settings)
			e.reconcileSerializableWritesLocked(t)
			e.enrollSerializableLocked(t.Ref, t.Isolation)
			t.TruncateSavepoints(position + 1)
			t.State = session.TxActive
		case parser.ReleaseSavepoint:
			// PostgreSQL restores the parent's access mode on subcommit, unlike
			// ordinary SESSION/LOCAL settings, whose effects are retained.
			t.ReadOnly = mark.ReadOnly
			t.Session.Settings.CurrentReadOnly = t.ReadOnly
			t.TruncateSavepoints(position)
		}
		e.reconcileReplicationOriginsLocked(t)
		s.controlOutcome = controlExecuted
		return Result{}, nil
	}()
	if err != nil {
		return result, err
	}
	if control.Kind == parser.RollbackSavepoint {
		e.Locks.Restore(t.Ticket, mark.Locks)
	}
	return Result{}, nil
}
