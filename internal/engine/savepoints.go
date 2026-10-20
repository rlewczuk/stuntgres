package engine

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func captureSavepoint(t *transactionOwner, name string) savepointMark {
	writes := maps.Clone(t.writes)
	t.savepointSerial++
	for a, rows := range t.writes {
		// A later append after rollback must not overwrite a discarded suffix
		// still retained by a result opened before rollback.
		writes[a] = rows[:len(rows):len(rows)]
	}
	e := engineOf(t.session)
	e.mu.Lock()
	projection := e.exclusionPending[t.ticket]
	e.mu.Unlock()
	t.session.Mu.Lock()
	identity := t.session.Authorization.state
	t.session.Mu.Unlock()
	return savepointMark{name: name, serial: t.savepointSerial, writes: writes, deletes: t.deletes, catalog: t.catalog, settings: t.session.Settings.savepoint(), isolation: t.isolation, readOnly: t.readOnly, deferrable: t.deferrable, locks: e.locks.Checkpoint(t.ticket), cursors: payloadOf(t.session).cursors.ordinal, exclusion: exclusionMarkSlotOf(ownerExtensionOrZero(t).exclusionModes, projection), identity: identity, notificationActions: t.notificationActions[:len(t.notificationActions):len(t.notificationActions)], notificationEvents: t.notificationEvents[:len(t.notificationEvents):len(t.notificationEvents)]}
}

func restoreSavepoint(t *transactionOwner, mark savepointMark, settings settingsMark) {
	ownerExtensionOf(t).detachCursors(payloadOf(t.session).cursors.rollback(mark.cursors))
	retireProtocolPortalsAfter(t, mark.cursors)
	t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
	t.notificationActions, t.notificationEvents = mark.notificationActions, mark.notificationEvents
	t.session.Settings.restoreSavepoint(settings, mark.identity)
	t.isolation, t.readOnly = mark.isolation, mark.readOnly
	t.deferrable = mark.deferrable
	syncCharacteristics(t)
	e := engineOf(t.session)
	e.mu.Lock()
	exclusion := exclusionMarkOrZero(mark.exclusion)
	ownerExtensionOf(t).exclusionModes = exclusion.modes
	if exclusion.projection == nil {
		delete(e.exclusionPending, t.ticket)
	} else {
		if e.exclusionPending == nil {
			e.exclusionPending = make(map[*lock.Ticket]*exclusionProjection)
		}
		e.exclusionPending[t.ticket] = exclusion.projection
	}
	e.exclusionGeneration++
	e.reconcileSerializableWritesLocked(t)
	e.mu.Unlock()
}

// Called owner-first. Preparation intentionally performs no name lookup.
func (s *StatementScope) gateSavepoint(control *parser.SavepointControl) error {
	t, e := s.owner, s.engine()
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.message {
		if err := s.admitTransactionLocked(e); err != nil {
			return err
		}
	}
	if err := e.endPreflight(s.op); err != nil {
		return err
	}
	if t.state == TxAborted && control.Kind != parser.RollbackSavepoint {
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
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == TxIdle || t.state == TxImplicit {
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
	var mark savepointMark
	var settings settingsMark
	var stack []savepointMark
	if control.Kind == parser.CreateSavepoint {
		if t.savepointSerial == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: foreign savepoint identity exhausted")
		}
		mark = captureSavepoint(t, control.Name.Text)
		// Reserve the stack before the final callback-free preflight.
		stack = slices.Grow(t.savepoints, 1)
	} else {
		for i := len(t.savepoints) - 1; i >= 0; i-- {
			if t.savepoints[i].name == control.Name.Text {
				index = i
				break
			}
		}
		if index >= 0 {
			mark = t.savepoints[index]
			if control.Kind == parser.RollbackSavepoint {
				settings = mark.settings.clone()
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
	t.mu.Unlock()
	hookErr := foreignSavepoint(t, s.op.Context(), kind, mark.serial)
	t.mu.Lock()
	if hookErr != nil {
		return Result{}, hookErr
	}
	result, err := func() (Result, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		t.session.Settings.mu.Lock()
		defer t.session.Settings.mu.Unlock()
		s.op.Session.Mu.Lock()
		defer s.op.Session.Mu.Unlock()
		if err := publicationOperationLocked(e, s.op); err != nil {
			return Result{}, err
		}
		if err := t.validate(t.ref); err != nil {
			return Result{}, err
		}
		if t.ref.Epoch() != e.catalog.state.Epoch() {
			return Result{}, ids.ErrCatalogReplaced
		}
		if control.Kind != parser.CreateSavepoint && index < 0 {
			return Result{}, sqlError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
		}
		if (t.identityCaptured || t.session.Settings.roleAction.present) && (control.Kind == parser.CreateSavepoint || control.Kind == parser.RollbackSavepoint) {
			// Reserve failed-preflight restoration and remaining boundaries.
			if err := roleRevisionHeadroom(t.session.Authorization, uint64(len(t.savepoints))+3); err != nil {
				return Result{}, err
			}
		}
		switch control.Kind {
		case parser.CreateSavepoint:
			t.savepoints = append(stack, mark)
		case parser.RollbackSavepoint:
			ownerExtensionOf(t).detachCursors(payloadOf(t.session).cursors.rollback(mark.cursors))
			retireProtocolPortalsAfter(t, mark.cursors)
			t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
			t.notificationActions, t.notificationEvents = mark.notificationActions, mark.notificationEvents
			targetIdentity := mark.identity
			targetIdentity.CurrentUser = settings.currentRole
			t.session.Settings.restoreIdentityLocked(targetIdentity)
			t.session.Settings.values, t.session.Settings.typed, t.session.Settings.actions = settings.values, settings.typed, settings.actions
			t.session.Settings.roleAction = settings.roleAction
			t.session.PublishTimeoutSettingsLocked(t.session.Settings, false, time.Now())
			t.isolation, t.readOnly = mark.isolation, mark.readOnly
			t.deferrable = mark.deferrable
			t.session.Settings.currentIsolation = isolationText(t.isolation)
			t.session.Settings.currentReadOnly = t.readOnly
			t.session.Settings.currentDeferrable = t.deferrable
			exclusion := exclusionMarkOrZero(mark.exclusion)
			ownerExtensionOf(t).exclusionModes = exclusion.modes
			if exclusion.projection == nil {
				delete(e.exclusionPending, t.ticket)
			} else {
				e.exclusionPending[t.ticket] = exclusion.projection
			}
			e.exclusionGeneration++
			e.reconcileSerializableWritesLocked(t)
			e.enrollSerializableLocked(t.ref, t.isolation)
			t.truncateSavepoints(index + 1)
			t.state = TxActive
		case parser.ReleaseSavepoint:
			// PostgreSQL restores the parent's access mode on subcommit, unlike
			// ordinary SESSION/LOCAL settings, whose effects are retained.
			t.readOnly = mark.readOnly
			t.session.Settings.currentReadOnly = t.readOnly
			t.truncateSavepoints(index)
		}
		s.controlOutcome = controlExecuted
		return Result{}, nil
	}()
	if err != nil {
		return result, err
	}
	if control.Kind == parser.RollbackSavepoint {
		e.locks.Restore(t.ticket, mark.locks)
	}
	return Result{}, nil
}

// exclusionMark is the exclusion constraint state a savepoint captured: the
// owner's exclusion modes and the engine's pending exclusion projection.
type exclusionMark struct {
	modes      exclusionModes
	projection *exclusionProjection
}

func (*exclusionMark) ExclusionMarkSlot() {}

// exclusionMarkSlotOf stores modes and projection. Exactly zero state (see
// exclusionModes.zero, nil projection) stays a nil slot, so restoring it
// reproduces the captured value.
func exclusionMarkSlotOf(modes exclusionModes, projection *exclusionProjection) session.ExclusionMarkSlot {
	if projection == nil && modes.zero() {
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
