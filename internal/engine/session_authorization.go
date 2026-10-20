package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type sessionAuthorization struct {
	session  *Session
	state    authorization.RoleState
	revision uint64
}

func (a *sessionAuthorization) EndSession(token ids.SessionToken, reason EndReason) error {
	if a.session == nil || a.session.SessionToken != token {
		return fmt.Errorf("stuntgres: foreign authorization attachment")
	}
	a.session.Mu.Lock()
	defer a.session.Mu.Unlock()
	if a.session.Authorization == a {
		a.session.Authorization = nil
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
	return s.Authorization.state
}
