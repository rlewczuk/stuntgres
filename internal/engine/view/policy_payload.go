package view

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
)

// PolicyPayloadOf stores e as a payload; a nil condition stays a nil payload.
func PolicyPayloadOf(e *stored.Policy) catalog.PolicyPayload {
	if e == nil {
		return nil
	}
	return e
}
