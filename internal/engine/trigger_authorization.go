package engine

import "stuntgres/internal/parser"

// Reached trigger expressions borrow the existing RI action actor, not the
// trigger function owner. Ordinary statements have no installed RI actor.
// Engine.mu is held by all callers.
func (s *StatementScope) statementAuthorizationLocked(stmt parser.Statement) (AuthorizationView, error) {
	auth, err := s.engine().statementAuthorizationLocked(s.op, stmt)
	if err == nil && s.foreignActionAuth != nil {
		auth.State.CurrentUser = s.foreignActionAuth.State.CurrentUser
	}
	return auth, err
}

func (s *StatementScope) routineAuthorizationLocked() (AuthorizationView, error) {
	auth, err := s.engine().authorizationLocked(s.op)
	if err == nil && s.foreignActionAuth != nil {
		auth.State.CurrentUser = s.foreignActionAuth.State.CurrentUser
	}
	return auth, err
}
