package core

import "stuntgres/internal/parser"

// Reached trigger expressions borrow the existing RI action actor, not the
// trigger function owner. Ordinary statements have no installed RI actor.
// Engine.mu is held by all callers.
func (s *StatementScope) statementAuthorizationLocked(stmt parser.Statement) (AuthorizationView, error) {
	auth, err := s.Engine().StatementAuthorizationLocked(s.Op, stmt)
	if err == nil && s.ForeignActionAuth != nil {
		auth.State.CurrentUser = s.ForeignActionAuth.State.CurrentUser
	}
	return auth, err
}

func (s *StatementScope) routineAuthorizationLocked() (AuthorizationView, error) {
	auth, err := s.Engine().AuthorizationLocked(s.Op)
	if err == nil && s.ForeignActionAuth != nil {
		auth.State.CurrentUser = s.ForeignActionAuth.State.CurrentUser
	}
	return auth, err
}
