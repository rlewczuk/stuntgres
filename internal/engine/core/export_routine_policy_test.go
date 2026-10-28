package core

import "stuntgres/internal/engine/catalog"

// ActivateRoutineCommandPolicy exposes the lexical activation to ownership tests.
func ActivateRoutineCommandPolicy(s *StatementScope, d *catalog.Procedure, constantPlanning bool) func() {
	return s.activateRoutineCommandPolicy(d, constantPlanning)
}
