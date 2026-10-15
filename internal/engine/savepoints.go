package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"time"
)

// Marks retain immutable logical roots and held lock contributions, not
// generator counters or snapshots. The outer block keeps the same ticket.
type savepointMark struct {
	name                string
	writes              map[ObjectAddress][]rowVersion
	deletes             map[ObjectAddress]*privateDeletionRoot
	catalog             *catalogChanges
	settings            settingsMark
	isolation           Isolation
	readOnly            bool
	deferrable          bool
	locks               relationLockCheckpoint
	cursors             uint64
	exclusionModes      exclusionModes
	exclusionProjection *exclusionProjection
	identity            authorization.RoleState
}

type settingsMark struct {
	values      map[string]string
	typed       map[string]settingValue
	actions     map[string]settingAction
	roleAction  roleSettingAction
	currentRole authorization.PrincipalRef
}

func (m settingsMark) clone() settingsMark {
	m.values, m.typed, m.actions = maps.Clone(m.values), maps.Clone(m.typed), maps.Clone(m.actions)
	return m
}

func (s *sessionSettings) savepoint() settingsMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := settingsMark{values: s.values, typed: s.typed, actions: s.actions, roleAction: s.roleAction}
	if s.session != nil {
		s.session.mu.Lock()
		if s.session.authorization != nil {
			m.currentRole = s.session.authorization.state.CurrentUser
		}
		s.session.mu.Unlock()
	}
	return m.clone()
}

// candidate is already independent of the retained mark; no callbacks or
// allocation are allowed between final preflight and effect installation.
func (s *sessionSettings) restoreSavepoint(candidate settingsMark, identity authorization.RoleState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values, s.typed, s.actions = candidate.values, candidate.typed, candidate.actions
	s.roleAction = candidate.roleAction
	if s.session != nil {
		s.session.mu.Lock()
		identity.CurrentUser = candidate.currentRole
		s.restoreIdentityLocked(identity)
		s.session.publishTimeoutSettingsLocked(s, false, time.Now())
		s.session.mu.Unlock()
	}
}

func (t *transactionOwner) captureSavepoint(name string) savepointMark {
	writes := maps.Clone(t.writes)
	for a, rows := range t.writes {
		// A later append after rollback must not overwrite a discarded suffix
		// still retained by a result opened before rollback.
		writes[a] = rows[:len(rows):len(rows)]
	}
	e := t.session.Engine()
	e.mu.Lock()
	projection := e.exclusionPending[t.ticket]
	e.mu.Unlock()
	t.session.mu.Lock()
	identity := t.session.authorization.state
	t.session.mu.Unlock()
	return savepointMark{name: name, writes: writes, deletes: t.deletes, catalog: t.catalog, settings: t.session.settings.savepoint(), isolation: t.isolation, readOnly: t.readOnly, deferrable: t.deferrable, locks: e.locks.checkpoint(t.ticket), cursors: t.session.cursors.ordinal, exclusionModes: t.exclusionModes, exclusionProjection: projection, identity: identity}
}

func (t *transactionOwner) restoreSavepoint(mark savepointMark, settings settingsMark) {
	t.cursorCleanup = append(t.cursorCleanup, t.session.cursors.rollback(mark.cursors)...)
	t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
	t.session.settings.restoreSavepoint(settings, mark.identity)
	t.isolation, t.readOnly = mark.isolation, mark.readOnly
	t.deferrable = mark.deferrable
	t.syncCharacteristics()
	e := t.session.Engine()
	e.mu.Lock()
	t.exclusionModes = mark.exclusionModes
	if mark.exclusionProjection == nil {
		delete(e.exclusionPending, t.ticket)
	} else {
		if e.exclusionPending == nil {
			e.exclusionPending = make(map[*relationTicket]*exclusionProjection)
		}
		e.exclusionPending[t.ticket] = mark.exclusionProjection
	}
	e.exclusionGeneration++
	e.reconcileSerializableWritesLocked(t)
	e.mu.Unlock()
}

func (t *transactionOwner) truncateSavepoints(n int) {
	clear(t.savepoints[n:])
	t.savepoints = t.savepoints[:n]
}

// Called owner-first. Preparation intentionally performs no name lookup.
func (s *StatementScope) gateSavepoint(control *parser.SavepointControl) error {
	t, e := s.owner, s.op.session.Engine()
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
	t, e := s.owner, s.op.session.Engine()
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
		mark = t.captureSavepoint(control.Name.Text)
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
	result, err := func() (Result, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		t.session.settings.mu.Lock()
		defer t.session.settings.mu.Unlock()
		s.op.session.mu.Lock()
		defer s.op.session.mu.Unlock()
		if err := publicationOperationLocked(e, s.op); err != nil {
			return Result{}, err
		}
		if err := t.validate(t.ref); err != nil {
			return Result{}, err
		}
		if t.ref.epoch != e.state.epoch {
			return Result{}, ErrCatalogReplaced
		}
		if control.Kind != parser.CreateSavepoint && index < 0 {
			return Result{}, sqlError("3B001", fmt.Sprintf("savepoint \"%s\" does not exist", control.Name.Text))
		}
		if (t.identityCaptured || t.session.settings.roleAction.present) && (control.Kind == parser.CreateSavepoint || control.Kind == parser.RollbackSavepoint) {
			// Reserve failed-preflight restoration and remaining boundaries.
			if err := roleRevisionHeadroom(t.session.authorization, uint64(len(t.savepoints))+3); err != nil {
				return Result{}, err
			}
		}
		switch control.Kind {
		case parser.CreateSavepoint:
			t.savepoints = append(stack, mark)
		case parser.RollbackSavepoint:
			t.cursorCleanup = append(t.cursorCleanup, t.session.cursors.rollback(mark.cursors)...)
			t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
			targetIdentity := mark.identity
			targetIdentity.CurrentUser = settings.currentRole
			t.session.settings.restoreIdentityLocked(targetIdentity)
			t.session.settings.values, t.session.settings.typed, t.session.settings.actions = settings.values, settings.typed, settings.actions
			t.session.settings.roleAction = settings.roleAction
			t.session.publishTimeoutSettingsLocked(t.session.settings, false, time.Now())
			t.isolation, t.readOnly = mark.isolation, mark.readOnly
			t.deferrable = mark.deferrable
			t.session.settings.currentIsolation = isolationText(t.isolation)
			t.session.settings.currentReadOnly = t.readOnly
			t.session.settings.currentDeferrable = t.deferrable
			t.exclusionModes = mark.exclusionModes
			if mark.exclusionProjection == nil {
				delete(e.exclusionPending, t.ticket)
			} else {
				e.exclusionPending[t.ticket] = mark.exclusionProjection
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
			t.session.settings.currentReadOnly = t.readOnly
			t.truncateSavepoints(index)
		}
		s.controlOutcome = controlExecuted
		return Result{}, nil
	}()
	if err != nil {
		return result, err
	}
	if control.Kind == parser.RollbackSavepoint {
		e.locks.restore(t.ticket, mark.locks)
	}
	return Result{}, nil
}
