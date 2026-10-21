package engine

import (
	"stuntgres/internal/engine/catalog"
)

// policyPayloadOf stores e as a payload; a nil condition stays a nil payload.
func policyPayloadOf(e *policyExpression) catalog.PolicyPayload {
	if e == nil {
		return nil
	}
	return e
}
