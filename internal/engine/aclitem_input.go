package engine

import (
	"stuntgres/internal/engine/session"
)

func (e *Engine) captureACLItemInput(auth AuthorizationView, op *session.Operation) capturedACLItemInput {
	return capturedACLItemInput{Roles: auth.Roles, Authority: &e.roles.aclAuthority, Op: op}
}
