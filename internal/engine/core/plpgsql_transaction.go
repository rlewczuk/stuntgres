package core

import (
	"time"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

func (s *StatementScope) plSubtransactionActive() bool {
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	for _, mark := range s.Owner.Savepoints {
		if mark.Internal {
			return true
		}
	}
	return false
}

func (h *plHost) Transaction(in proc.PLInstruction) error {
	s := h.s
	commit := in.Transaction.Kind == parser.KindCommit
	action, verb := "at ROLLBACK", "roll back"
	if commit {
		action, verb = "at COMMIT", "commit"
	}
	failure := func(err error) error {
		return s.plRuntimeError(err, h.read, h.d, parser.Position{}, parser.Position{}, in.Syntax.Pos, action, false)
	}
	if s.plAtomic {
		return failure(SQLError("2D000", "invalid transaction termination"))
	}
	if s.plSubtransactionActive() {
		return failure(SQLError("2D000", "cannot "+verb+" while a subtransaction is active"))
	}
	if err := s.promotePLLoops(); err != nil {
		return failure(err)
	}
	if err := s.rotatePLTransaction(commit, in.Transaction.Chain); err != nil {
		return failure(err)
	}
	return nil
}

// Check each source immediately before holding it, stopping at the first
// failure. Rollback preserves pinned iteration just as commit does.
func (s *StatementScope) promotePLLoops() error {
	ref := s.TxRef
	registry := PayloadOf(s.Op.Session).Cursors
	for q := s.plLoops; q != nil; q = q.previous {
		if !q.readOnly {
			return SQLError("55000", "cannot perform transaction commands inside a cursor loop that is not read-only")
		}
		if err := q.promote(); err != nil {
			return err
		}
	}
	for _, c := range registry.Entries {
		if c.pinned && !c.promoted && c.tx == ref {
			if !c.plReadOnly {
				return SQLError("55000", "cannot perform transaction commands inside a cursor loop that is not read-only")
			}
			restore := s.ActivateSQLRoutine(nil, c.catalog)
			err := s.materializeHeldCursor(c)
			if err = restore(err); err != nil {
				return err
			}
		}
	}
	return nil
}

// rotatePLTransaction borrows the root's admission and publication owners. It
// neither finishes the root scope nor refreshes its statement timeout budget.
func (s *StatementScope) rotatePLTransaction(commit, chain bool) error {
	e, t := s.Engine(), s.Owner
	if commit {
		if err := s.acquireAlterCatalogNames(); err != nil {
			return err
		}
		if err := s.completeCommandEffects(true, false); err != nil {
			return err
		}
		if err := s.finishForeignModifications(nil); err != nil {
			return err
		}
		for _, program := range s.rlsPrograms {
			if err := program.FinishSources(nil); err != nil {
				return err
			}
		}
		if s.Catalog != nil {
			e.Mu.Lock()
			err := s.AcquireJournalRoleDependenciesLocked(s.Catalog)
			e.Mu.Unlock()
			if err != nil {
				return err
			}
		}
		t.Mu.Lock()
		catalog := t.Catalog
		if s.Catalog != nil {
			catalog = s.Catalog
		}
		err := s.mergeSegmentLocked(catalog)
		t.Mu.Unlock()
		if err != nil {
			return err
		}
	} else {
		if err := s.finishForeignModifications(nil); err != nil {
			return err
		}
		if err := s.finishForeignStatements(nil); err != nil {
			return err
		}
	}
	if s.ReplacementRead {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	restart := restartPL
	if chain {
		restart = restartPLChain
	}
	_, err := e.endAdmitted(s.Op, t, s.TxRef, commit, restart, true, time.Now().UTC().Truncate(time.Microsecond), s)
	if err != nil {
		return err
	}
	e.endStatementVisibility(s.TxRef, false)
	s.resetPLSegment()
	t.Mu.Lock()
	s.attachTransactionLocked()
	t.Mu.Unlock()
	if p := s.plExecutingPortal; p != nil {
		p.Transaction = s.TxRef
		p.Retired = false
	}
	return drainCursorCleanup(t, s.executionContext(), s)
}

func (s *StatementScope) resetPLSegment() {
	for _, program := range s.rlsPrograms {
		program.Close()
	}
	s.rlsPrograms, s.rlsActive = nil, nil
	s.Input, s.Output = s.Op.EffectiveSettings().Presentation()
	s.installSQLRoutineScratch(s.rebaseRoutineScratch(s.sqlRoutineScratch()))
	s.Stage.Rows, s.Stage.Deletes = nil, nil
	s.Stage.Replacing = false
	s.Catalog, s.CatalogAuthority = nil, nil
	s.ProjectedRoots = false
	s.currentSnapshot, s.snapshotFrontier = nil, 0
	s.snapshotTarget, s.snapshotTargets = visibility.Root{}, nil
	s.notificationActions, s.notificationEvents = nil, nil
	s.multiEvents, s.alterNames = nil, nil
	s.routinePrograms, s.checkPrograms = nil, nil
	s.Domains.endAdmission()
	s.partitionsSlot.DropPrograms()
	s.indexesSlot.EndStatement()
	s.Inheritance.endStatement()
	s.foreignActionsSlot, s.ForeignKeysSlot, s.DropsSlot, s.truncateSlot = nil, nil, nil, nil
	s.foreignTablesSlot = nil
	s.Completed, s.sessionCompleted = false, false
	s.controlOutcome = controlOrdinary
	s.effectCommand = 0
}
