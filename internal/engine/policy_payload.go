package engine

import "stuntgres/internal/engine/ids"

// policyPayload is a stored USING or WITH CHECK condition with its frozen
// presentation, as policyDefinition holds it. Catalog code reads only its
// dependencies and validation; the concrete *policyExpression, whose
// validation walks engine expression nodes, stays in the engine behind
// policyExpressionOf.
type policyPayload interface {
	// DependsOn reports whether the condition reads target. A zero SubID
	// matches the whole object.
	DependsOn(target ids.ObjectAddress) bool
	// Validate checks the condition against the relation that owns the policy.
	Validate(v CatalogRead, owner *relationDescriptor) error
}

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
