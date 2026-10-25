package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// RebindAlterTypeChecks rebinds the affected CHECKs under Engine.mu.
func RebindAlterTypeChecks(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, ref ids.ObjectRef, next *catalog.Relation, queue []table.AlterTypeCheck) (*catalog.Relation, []catalog.CheckConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.RebindAlterTypeChecks(AlterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, ref, next, queue)
}
