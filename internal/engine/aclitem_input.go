package engine

import (
	"stuntgres/internal/engine/session"
)

func (e *Engine) captureACLItemInput(auth AuthorizationView, op *session.Operation) capturedACLItemInput {
	return capturedACLItemInput{roles: auth.Roles, authority: &e.roles.aclAuthority, op: op}
}
