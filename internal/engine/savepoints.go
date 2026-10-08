package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/parser"
)

// Marks retain immutable logical roots, not generator counters, snapshots or
// lock contributions. The existing ticket remains owned by the outer block.
type savepointMark struct {
	name      string
	writes    map[ObjectAddress][]rowVersion
	deletes   map[ObjectAddress]*privateDeletionRoot
	catalog   *catalogChanges
	settings  settingsMark
	isolation Isolation
	readOnly  bool
}

type settingsMark struct {
	values  map[string]string
	typed   map[string]settingValue
	actions map[string]settingAction
}

func (m settingsMark) clone() settingsMark {
	return settingsMark{maps.Clone(m.values), maps.Clone(m.typed), maps.Clone(m.actions)}
}

func (s *sessionSettings) savepoint() settingsMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (settingsMark{s.values, s.typed, s.actions}).clone()
}

// candidate is already independent of the retained mark; no callbacks or
// allocation are allowed between final preflight and effect installation.
func (s *sessionSettings) restoreSavepoint(candidate settingsMark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values, s.typed, s.actions = candidate.values, candidate.typed, candidate.actions
}

func (t *transactionOwner) captureSavepoint(name string) savepointMark {
	writes := maps.Clone(t.writes)
	for a, rows := range t.writes {
		// A later append after rollback must not overwrite a discarded suffix
		// still retained by a result opened before rollback.
		writes[a] = rows[:len(rows):len(rows)]
	}
	return savepointMark{name, writes, t.deletes, t.catalog, t.session.settings.savepoint(), t.isolation, t.readOnly}
}

func (t *transactionOwner) restoreSavepoint(mark savepointMark, settings settingsMark) {
	t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
	t.session.settings.restoreSavepoint(settings)
	t.isolation, t.readOnly = mark.isolation, mark.readOnly
	t.syncCharacteristics()
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
	if t.state == TxIdle {
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
	switch control.Kind {
	case parser.CreateSavepoint:
		t.savepoints = append(stack, mark)
	case parser.RollbackSavepoint:
		t.writes, t.deletes, t.catalog = mark.writes, mark.deletes, mark.catalog
		t.session.settings.values, t.session.settings.typed, t.session.settings.actions = settings.values, settings.typed, settings.actions
		t.isolation, t.readOnly = mark.isolation, mark.readOnly
		t.session.settings.currentIsolation = isolationText(t.isolation)
		t.session.settings.currentReadOnly = t.readOnly
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
}
