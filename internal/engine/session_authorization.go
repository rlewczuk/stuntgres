package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type sessionAuthorization struct {
	Session  *Session
	State    authorization.RoleState
	Revision uint64
}

func (a *sessionAuthorization) EndSession(token ids.SessionToken, reason EndReason) error {
	if a.Session == nil || a.Session.SessionToken != token {
		return fmt.Errorf("stuntgres: foreign authorization attachment")
	}
	a.Session.Mu.Lock()
	defer a.Session.Mu.Unlock()
	if a.Session.Authorization == a {
		a.Session.Authorization = nil
	}
	return nil
}

func (op *Operation) RoleState() authorization.RoleState {
	if op == nil || op.Self != op || op.Session == nil {
		return authorization.RoleState{}
	}
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Authorization == nil {
		return authorization.RoleState{}
	}
	return s.Authorization.State
}
