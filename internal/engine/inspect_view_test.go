package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// EngineOpenRelationSourceLocked calls x.openRelationSourceLocked.
func EngineOpenRelationSourceLocked(x *Engine, scope *StatementScope, ref ids.ObjectRef, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (*exec.Source, error) {
	return x.openRelationSourceLocked(scope, ref, invoker, mark, locks, outputUses)
}

// ScopeAcquireMaterializedRefresh calls x.acquireMaterializedRefresh.
func ScopeAcquireMaterializedRefresh(x *StatementScope, stmt *parser.RefreshMaterializedView) error {
	return x.acquireMaterializedRefresh(stmt)
}
