package core

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Rebuilding captured owners is the existing trusted native Restore boundary,
// not a general permission bypass for user DDL or arbitrary callers.
func (e *Engine) conversionCheckCreate(op *session.Operation, auth AuthorizationView, namespace ids.ObjectRef) error {
	err := auth.CheckCreate(namespace)
	if err == nil {
		return nil
	}
	denied, ok := err.(*sqlstate.Error)
	if op == nil || !OperationReplayOrZero(op).TableDestination || !ok || denied.Code != "42501" {
		return err
	}
	actor, actorErr := auth.Roles.Descriptor(auth.State.Authenticated)
	if actorErr != nil || !actor.Superuser || e.ImportTargets == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore conversion authority")
	}
	return nil
}
