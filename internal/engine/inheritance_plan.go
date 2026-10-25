package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
)

// inheritancePlan is the recursing ALTER's plan record, nil when the statement
// is not recursing through an inheritance tree. Its methods are nil-safe.
func inheritancePlan(scope *core.StatementScope) *table.InheritanceAlterPlan {
	if state := scope.Inheritance.Alter; state != nil {
		return &state.InheritanceAlterPlan
	}
	return nil
}
