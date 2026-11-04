package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
)

// planningExecutionBarrier preserves demanded runtime diagnostics through
// enclosing computational frontiers. Node-local FoldNever alone does not imply
// this transitive barrier: relational providers still preflight their inputs.
// Unlike literal-only eligibility, this does not reject typed parameter inputs.
// Dormant declaration binders have no live host: their installed crypto calls
// remain symbolic, including beneath enclosing casts/arrays/composites. A live
// host supplies planning readiness, never a cached permission or policy decision.
func planningExecutionBarrier(c *Context, node expr.Expr) bool {
	barrier := false
	dormant := c.LiveHost() == nil
	trusted := sepgsqlTrustedPlanning(c)
	plan.WalkExpression(node, func(child expr.Expr) {
		if _, ok := child.(expr.ExecutionDemand); ok {
			barrier = true
		}
		if _, crypto := child.(plan.InstalledCryptoExpr); crypto && dormant {
			barrier = true
		}
		if trusted && sepgsqlHookableExpression(child) {
			barrier = true
		}
	})
	return barrier
}

func sepgsqlTrustedPlanning(c *Context) bool {
	if c == nil {
		return false
	}
	if owner := c.Catalog.Owner(); owner != nil {
		return owner.Sepgsql.AnyTrustedTransitions()
	}
	if c.RoutineAuthority != nil {
		owner := c.RoutineAuthority.AdmissionAccess().Owner
		return owner != nil && owner.Sepgsql.AnyTrustedTransitions()
	}
	if host := c.LiveHost(); host != nil {
		auth, _, err := host.RoutineAdmission()
		if err != nil {
			return true
		} // Preserve executable diagnostics; never compute with ended authority.
		return auth.Owner != nil && auth.Owner.Sepgsql.AnyTrustedTransitions()
	}
	return false
}
func sepgsqlHookableExpression(node expr.Expr) bool {
	declaration, err := ExpressionRoutineDescriptor(node)
	return err != nil || declaration != nil && !routine.FmgrBuiltin(declaration)
}
func sepgsqlPlanningSensitive(c *Context, node expr.Expr) bool {
	if !sepgsqlTrustedPlanning(c) {
		return false
	}
	sensitive := false
	plan.WalkExpression(node, func(child expr.Expr) {
		if sepgsqlHookableExpression(child) {
			sensitive = true
		}
	})
	return sensitive
}
