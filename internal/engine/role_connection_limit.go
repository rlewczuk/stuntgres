package engine

import (
	"stuntgres/internal/engine/authorization"
)

// sessionMu owns both counting and publication/removal. Do not lock peers:
// teardown holds their own mutex before taking sessionMu.
func (e *Engine) roleSessionCountLocked(ref authorization.PrincipalRef) int {
	n := 0
	for _, s := range e.sessions {
		if s.CountedAuthenticated == ref {
			n++
		}
	}
	return n
}
