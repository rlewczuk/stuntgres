package engine

import "stuntgres/internal/engine/dml"

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
	if err := dml.SettleForeignActions(s.writeHost()); err != nil {
		return err
	}
	if s.foreignActionsOrZero().ExclusionAdded {
		if err := s.checkExclusionCommandEvents(allExclusions, nil, bounded); err != nil {
			return err
		}
	}
	if !s.projectedRoots {
		return dml.CheckForeignKeys(s.writeHost())
	}
	return nil
}
