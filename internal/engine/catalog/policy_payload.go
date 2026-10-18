package catalog

import (
	"stuntgres/internal/engine/ids"
)

// PolicyPayload is a stored USING or WITH CHECK condition with its frozen
// presentation, as PolicyDefinition holds it. Catalog code reads only its
// dependencies and validation; the concrete *policyExpression, whose
// validation walks engine expression nodes, stays in the engine behind
// policyExpressionOf.
type PolicyPayload interface {
	// DependsOn reports whether the condition reads target. A zero SubID
	// matches the whole object.
	DependsOn(target ids.ObjectAddress) bool
	// Validate checks the condition against the relation that owns the policy.
	Validate(v Read, owner *Relation) error
}
