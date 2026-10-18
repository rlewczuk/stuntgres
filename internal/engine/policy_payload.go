package engine

import "stuntgres/internal/engine/ids"

// policyExpressionOf returns p's concrete condition, or nil when p is absent.
func policyExpressionOf(p policyPayload) *policyExpression {
	e, _ := p.(*policyExpression)
	return e
}

// policyPayloadOf stores e as a payload; a nil condition stays a nil payload.
func policyPayloadOf(e *policyExpression) policyPayload {
	if e == nil {
		return nil
	}
	return e
}

func (e *policyExpression) DependsOn(target ids.ObjectAddress) bool {
	return e.presentation.DependsOn(target)
}

func (e *policyExpression) Validate(v CatalogRead, owner *relationDescriptor) error {
	return validatePolicyExpression(v, owner, e)
}
