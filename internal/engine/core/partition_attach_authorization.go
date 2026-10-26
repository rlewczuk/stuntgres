package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
)

// Private import executes the same utility against captured definitions. Only
// its authenticated bootstrap may check the independently owned attachments.
func (e *Engine) PartitionAttachAuthorization(op *session.Operation, auth AuthorizationView) (AuthorizationView, error) {
	if !OperationReplayOrZero(op).PartitionAttach {
		return auth, nil
	}
	if op.Self != op || op.Session == nil || EngineOf(op.Session) != e || e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	auth.State.CurrentUser = bootstrap.Ref
	return auth, nil
}
