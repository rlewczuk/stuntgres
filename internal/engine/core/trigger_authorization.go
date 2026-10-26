package core

import "stuntgres/internal/parser"

// Reached trigger expressions borrow the existing RI action actor, not the
// trigger function owner. Ordinary statements have no installed RI actor.
// Engine.mu is held by all callers.
func (s *StatementScope) statementAuthorizationLocked(stmt parser.Statement) (AuthorizationView, error) {
	auth, err := s.Engine().StatementAuthorizationLocked(s.Op, stmt)
	if err == nil && s.Op.Restricted == nil && s.ForeignActionAuth != nil {
		auth.State.CurrentUser = s.ForeignActionAuth.State.CurrentUser
	}
	return auth, err
}

// RoutineAuthorizationLocked selects the actor for routine and installed-function host admission.
// Rule relation authority belongs only to writes; an active restricted owner
// takes precedence over an enclosing RI actor without changing that write path.
func (s *StatementScope) RoutineAuthorizationLocked() (AuthorizationView, error) {
	auth, err := s.Engine().AuthorizationLocked(s.Op)
	if err == nil && s.Op.Restricted == nil && s.ForeignActionAuth != nil {
		auth.State.CurrentUser = s.ForeignActionAuth.State.CurrentUser
	}
	return auth, err
}

// callableAuthorizationLocked admits callable targets without allowing an
// enclosing relation action to replace an active security-restricted owner.
// Outside that frame, retain the host's existing rule/RI action authority.
// Genuine relation writes continue to use writeAuthorizationLocked directly.
func (s *StatementScope) callableAuthorizationLocked(e *Engine) (AuthorizationView, error) {
	if s.Op.Restricted != nil {
		return s.RoutineAuthorizationLocked()
	}
	return s.writeAuthorizationLocked(e)
}
