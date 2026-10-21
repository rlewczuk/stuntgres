package engine

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func captureSavepoint(t *session.TransactionOwner, name string) session.SavepointMark {
	writes := maps.Clone(t.Writes)
	t.SavepointSerial++
	for a, rows := range t.Writes {
		// A later append after rollback must not overwrite a discarded suffix
		// still retained by a result opened before rollback.
		writes[a] = rows[:len(rows):len(rows)]
	}
	e := engineOf(t.Session)
	e.mu.Lock()
	projection := e.exclusions.OwnLocked(t.Ticket)
	e.mu.Unlock()
	t.Session.Mu.Lock()
	identity := t.Session.Authorization.State
	t.Session.Mu.Unlock()
	return session.SavepointMark{Name: name, Serial: t.SavepointSerial, Writes: writes, Deletes: t.Deletes, Catalog: t.Catalog, Settings: t.Session.Settings.Savepoint(), Isolation: t.Isolation, ReadOnly: t.ReadOnly, Deferrable: t.Deferrable, Locks: e.locks.Checkpoint(t.Ticket), Cursors: payloadOf(t.Session).cursors.ordinal, Exclusion: exclusionMarkSlotOf(ownerExtensionOrZero(t).exclusionModes, projection), Identity: identity, NotificationActions: t.NotificationActions[:len(t.NotificationActions):len(t.NotificationActions)], NotificationEvents: t.NotificationEvents[:len(t.NotificationEvents):len(t.NotificationEvents)]}
}

func restoreSavepoint(t *session.TransactionOwner, mark session.SavepointMark, settings session.SettingsMark) {
	ownerExtensionOf(t).detachCursors(payloadOf(t.Session).cursors.rollback(mark.Cursors))
	retireProtocolPortalsAfter(t, mark.Cursors)
	t.Writes, t.Deletes, t.Catalog = mark.Writes, mark.Deletes, mark.Catalog
	t.NotificationActions, t.NotificationEvents = mark.NotificationActions, mark.NotificationEvents
	t.Session.Settings.RestoreSavepoint(settings, mark.Identity)
	t.Isolation, t.ReadOnly = mark.Isolation, mark.ReadOnly
	t.Deferrable = mark.Deferrable
	syncCharacteristics(t)
	e := engineOf(t.Session)
	e.mu.Lock()
	exclusion := exclusionMarkOrZero(mark.Exclusion)
	ownerExtensionOf(t).exclusionModes = exclusion.modes
	e.exclusions.RestoreLocked(t.Ticket, exclusion.projection)
	e.reconcileSerializableWritesLocked(t)
	e.mu.Unlock()
}

// Called owner-first. Preparation intentionally performs no name lookup.
func (s *StatementScope) gateSavepoint(control *parser.SavepointControl) error {
	t, e := s.owner, s.engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if s.message {
		if err := s.admitTransactionLocked(e); err != nil {
			return err
		}
	}
	if err := e.endPreflight(s.op); err != nil {
		return err
	}
	if t.State == session.TxAborted && control.Kind != parser.RollbackSavepoint {
		return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	return nil
}

func (s *StatementScope) executeSavepoint(control *parser.SavepointControl, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := checkStatementSignature(*s.Expected, StatementDescription{}); err != nil {
			return Result{}, err
		}
	}
	t, e := s.owner, s.engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.State == session.TxIdle || t.State == session.TxImplicit {
		word := "SAVEPOINT"
		if control.Kind == parser.RollbackSavepoint {
			word = "ROLLBACK TO SAVEPOINT"
		}
		if control.Kind == parser.ReleaseSavepoint {
			word = "RELEASE SAVEPOINT"
		}
		return Result{}, sqlError("25P01", word+" can only be used in transaction blocks")
	}
	index := -1
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
		for i := len(t.Savepoints) - 1; i >= 0; i-- {
			if t.Savepoints[i].Name == control.Name.Text {
				index = i
				break
			}
		}
		if index >= 0 {
			mark = t.Savepoints[index]
			if control.Kind == parser.RollbackSavepoint {
				settings = mark.Settings.Clone()
			}
		}
	}
	if control.Kind != parser.CreateSavepoint && index < 0 {
		return Result{}, sqlError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
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
	hookErr := foreignSavepoint(t, s.op.Context(), kind, mark.Serial)
	t.Mu.Lock()
	if hookErr != nil {
		return Result{}, hookErr
	}
	result, err := func() (Result, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		t.Session.Settings.Mu.Lock()
		defer t.Session.Settings.Mu.Unlock()
		s.op.Session.Mu.Lock()
		defer s.op.Session.Mu.Unlock()
		if err := publicationOperationLocked(e, s.op); err != nil {
			return Result{}, err
		}
		if err := t.Validate(t.Ref); err != nil {
			return Result{}, err
		}
		if t.Ref.Epoch() != e.catalog.state.Epoch() {
			return Result{}, ids.ErrCatalogReplaced
		}
		if control.Kind != parser.CreateSavepoint && index < 0 {
			return Result{}, sqlError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
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
			ownerExtensionOf(t).detachCursors(payloadOf(t.Session).cursors.rollback(mark.Cursors))
			retireProtocolPortalsAfter(t, mark.Cursors)
			t.Writes, t.Deletes, t.Catalog = mark.Writes, mark.Deletes, mark.Catalog
			t.NotificationActions, t.NotificationEvents = mark.NotificationActions, mark.NotificationEvents
			targetIdentity := mark.Identity
			targetIdentity.CurrentUser = settings.CurrentRole
			t.Session.Settings.RestoreIdentityLocked(targetIdentity)
			t.Session.Settings.Values, t.Session.Settings.Typed, t.Session.Settings.Actions = settings.Values, settings.Typed, settings.Actions
			t.Session.Settings.RoleAction = settings.RoleAction
			t.Session.PublishTimeoutSettingsLocked(t.Session.Settings, false, time.Now())
			t.Isolation, t.ReadOnly = mark.Isolation, mark.ReadOnly
			t.Deferrable = mark.Deferrable
			t.Session.Settings.CurrentIsolation = session.IsolationText(t.Isolation)
			t.Session.Settings.CurrentReadOnly = t.ReadOnly
			t.Session.Settings.CurrentDeferrable = t.Deferrable
			exclusion := exclusionMarkOrZero(mark.Exclusion)
			ownerExtensionOf(t).exclusionModes = exclusion.modes
			e.exclusions.RestoreLocked(t.Ticket, exclusion.projection)
			e.reconcileSerializableWritesLocked(t)
			e.enrollSerializableLocked(t.Ref, t.Isolation)
			t.TruncateSavepoints(index + 1)
			t.State = session.TxActive
		case parser.ReleaseSavepoint:
			// PostgreSQL restores the parent's access mode on subcommit, unlike
			// ordinary SESSION/LOCAL settings, whose effects are retained.
			t.ReadOnly = mark.ReadOnly
			t.Session.Settings.CurrentReadOnly = t.ReadOnly
			t.TruncateSavepoints(index)
		}
		s.controlOutcome = controlExecuted
		return Result{}, nil
	}()
	if err != nil {
		return result, err
	}
	if control.Kind == parser.RollbackSavepoint {
		e.locks.Restore(t.Ticket, mark.Locks)
	}
	return Result{}, nil
}

// exclusionMark is the exclusion constraint state a savepoint captured: the
// owner's exclusion modes and the engine's pending exclusion projection.
type exclusionMark struct {
	modes      index.ExclusionModes
	projection *index.ExclusionProjection
}

func (*exclusionMark) ExclusionMarkSlot() {}

// exclusionMarkSlotOf stores modes and projection. Exactly zero state (see
// index.ExclusionModes.Zero, nil projection) stays a nil slot, so restoring it
// reproduces the captured value.
func exclusionMarkSlotOf(modes index.ExclusionModes, projection *index.ExclusionProjection) session.ExclusionMarkSlot {
	if projection == nil && modes.Zero() {
		return nil
	}
	return &exclusionMark{modes: modes, projection: projection}
}

// exclusionMarkOrZero returns the captured exclusion state; a nil slot is zero.
func exclusionMarkOrZero(s session.ExclusionMarkSlot) exclusionMark {
	if m, _ := s.(*exclusionMark); m != nil {
		return *m
	}
	return exclusionMark{}
}
