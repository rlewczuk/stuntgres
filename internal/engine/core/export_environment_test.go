package core

import "stuntgres/internal/engine/catalog"

// ScopeEnterRoutineEnvironment exercises the real entry/unwind operation.
func ScopeEnterRoutineEnvironment(s *StatementScope, d *catalog.Procedure) (func(error) error, error) {
	return s.enterRoutineEnvironment(d)
}
