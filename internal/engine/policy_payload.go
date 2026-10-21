package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// policyExpressionOf returns p's concrete condition, or nil when p is absent.
func policyExpressionOf(p catalog.PolicyPayload) *policyExpression {
	e, _ := p.(*policyExpression)
	return e
}

// policyPayloadOf stores e as a payload; a nil condition stays a nil payload.
func policyPayloadOf(e *policyExpression) catalog.PolicyPayload {
	if e == nil {
		return nil
	}
	return e
}

func (e *policyExpression) DependsOn(target ids.ObjectAddress) bool {
	return e.presentation.DependsOn(target)
}

func (e *policyExpression) Validate(v catalog.Read, owner *catalog.Relation) error {
	return validatePolicyExpression(v, owner, e)
}

func (e *policyExpression) VisitExpression(visit func(expr.Expr) error) error {
	return visit(e.expression)
}
