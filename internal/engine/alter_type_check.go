package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// rebindAlterTypeChecks rebinds the affected CHECKs under Engine.mu.
func (e *Engine) rebindAlterTypeChecks(scope *StatementScope, op *session.Operation, auth AuthorizationView, ref ids.ObjectRef, next *catalog.Relation, queue []table.AlterTypeCheck) (*catalog.Relation, []catalog.CheckConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.RebindAlterTypeChecks(alterHost{ddlHost{e: e, op: op}, scope}, auth, ref, next, queue)
}
