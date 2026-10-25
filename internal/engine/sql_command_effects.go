package engine

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
	if err := s.checkExclusionCommandEvents(allExclusions, nil, bounded); err != nil {
		return err
	}
	if err := dml.SettleForeignActions(s.WriteHost()); err != nil {
		return err
	}
	if s.foreignActionsOrZero().ExclusionAdded {
		if err := s.checkExclusionCommandEvents(allExclusions, nil, bounded); err != nil {
			return err
		}
	}
	if !s.projectedRoots {
		return dml.CheckForeignKeys(s.WriteHost())
	}
	return nil
}

// Nested SQL commands settle their own effects without publishing the root.
// Call the returned completion before executing a query-loop body.
func (s *StatementScope) beginRoutineCommand() (func(error) error, error) {
	if s.effectSerial == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: SQL command identity exhausted")
	}
	previous := s.effectCommand
	s.effectSerial++
	s.effectCommand = s.effectSerial
	return func(primary error) error {
		if primary == nil {
			primary = s.completeCommandEffects(false, true)
		}
		s.effectCommand = previous
		return primary
	}, nil
}
