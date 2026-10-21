package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// PolicyPayload is a stored USING or WITH CHECK condition with its frozen
// presentation, as PolicyDefinition holds it. Catalog code reads its
// dependencies, its expression and its validation; the concrete
// *policyExpression, whose validation walks engine expression nodes, stays in
// the engine behind policyExpressionOf.
type PolicyPayload interface {
	// DependsOn reports whether the condition reads target. A zero SubID
	// matches the whole object.
	DependsOn(target ids.ObjectAddress) bool
	// Validate checks the condition against the relation that owns the policy.
	Validate(v Read, owner *Relation) error
	// VisitExpression calls visit with the condition's bound expression and
	// returns its error.
	VisitExpression(visit func(expr.Expr) error) error
}

// VisitExpressions visits p's USING condition, then its WITH CHECK
// condition, skipping absent ones, and stops at the first error.
func (p PolicyDefinition) VisitExpressions(visit func(expr.Expr) error) error {
	for _, condition := range []PolicyPayload{p.Using, p.Check} {
		if condition != nil {
			if err := condition.VisitExpression(visit); err != nil {
				return err
			}
		}
	}
	return nil
}
