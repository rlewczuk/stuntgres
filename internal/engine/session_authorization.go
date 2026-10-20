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
	if a.session == nil || a.session.token != token {
		return fmt.Errorf("stuntgres: foreign authorization attachment")
	}
	a.session.mu.Lock()
	defer a.session.mu.Unlock()
	if a.session.authorization == a {
		a.session.authorization = nil
	}
	return nil
}

func (op *Operation) RoleState() authorization.RoleState {
	if op == nil || op.self != op || op.session == nil {
		return authorization.RoleState{}
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorization == nil {
		return authorization.RoleState{}
	}
	return s.authorization.state
}
