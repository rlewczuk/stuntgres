package core

import (
	"fmt"
	"stuntgres/internal/engine/dml"
)

// Complete only the caller's accepted command events. Visibility remains the
// full owner/stage view; an outer producer can be suspended with unfinished
// obligations while a nested SQL command completes. No publication or Finish.
func (s *StatementScope) completeCommandEffects(allExclusions, bounded bool) error {
	if scope := s.foreignActionsSlot; scope != nil {
		previous := scope.Command
		previousJobs := scope.Jobs
		scope.Command = s.effectCommand
		defer func() { scope.Command, scope.Jobs = previous, previousJobs }()
	}
	for {
		if err := s.qualifyAfterStatements(); err != nil {
			return err
		}
		if err := s.checkConstraintEventPhase(allExclusions, nil, bounded, false); err != nil {
			return err
		}
		if err := dml.SettleForeignActions(s.ActivateRuntime(s.executionContext()), s.WriteHost()); err != nil {
			return err
		}
		if s.foreignActionsOrZero().ExclusionAdded {
			if err := s.checkConstraintEventPhase(allExclusions, nil, bounded, false); err != nil {
				return err
			}
		}
		if !s.ProjectedRoots {
			if err := dml.CheckForeignKeys(s.WriteHost()); err != nil {
				return err
			}
		}
		if err := s.checkConstraintEventPhase(allExclusions, nil, bounded, true); err != nil {
			return err
		}
		if s.afterTriggers == nil {
			return nil
		}
		f := s.afterTriggers.frames[s.effectCommand]
		if f == nil || f.draining || f.row == len(f.rows) && f.statement == len(f.statements) {
			return nil
		}
		if err := s.drainAfter(); err != nil {
			return err
		}
		// Settle any immediate effects added during firing before returning.
	}
}

// BeginRoutineCommand starts a nested SQL command's effect scope without
// publishing the root. Complete it before resuming the suspended caller.
func (s *StatementScope) BeginRoutineCommand() (func(error) error, error) {
	if s.effectSerial == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: SQL command identity exhausted")
	}
	previous := s.effectCommand
	ref := s.TxRef
	s.effectSerial++
	s.effectCommand = s.effectSerial
	return func(primary error) error {
		if s.TxRef != ref {
			return primary
		}
		if primary == nil {
			primary = s.completeCommandEffects(false, true)
		}
		if s.afterTriggers != nil {
			delete(s.afterTriggers.frames, s.effectCommand)
		}
		s.effectCommand = previous
		return primary
	}, nil
}
