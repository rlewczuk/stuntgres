package core

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

func (e *Engine) CaptureACLItemInput(auth AuthorizationView, op *session.Operation) regobj.ACLItemInput {
	return regobj.ACLItemInput{Roles: auth.Roles, Authority: &e.RoleStore.ACLAuthority, Op: op}
}
