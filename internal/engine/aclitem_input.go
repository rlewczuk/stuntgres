package engine

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

func (e *Engine) captureACLItemInput(auth AuthorizationView, op *session.Operation) regobj.ACLItemInput {
	return regobj.ACLItemInput{Roles: auth.Roles, Authority: &e.roles.aclAuthority, Op: op}
}
