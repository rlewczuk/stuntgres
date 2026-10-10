package engine

import "stuntgres/internal/sqlstate"

// sessionMu owns both counting and publication/removal. Do not lock peers:
// teardown holds their own mutex before taking sessionMu.
func (e *Engine) roleSessionCountLocked(ref PrincipalRef) int {
	n := 0
	for _, s := range e.sessions {
		if s.countedAuthenticated == ref {
			n++
		}
	}
	return n
}
func roleConnectionLimitError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: "too many connections for role \"" + name + "\""}
}

func roleLoginError(name string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: "role \"" + name + "\" is not permitted to log in"}
}
