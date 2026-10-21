package engine

import "stuntgres/internal/engine/exec"

// AdmissionAccess is the routine admission view of the borrowed authority.
func (v *AuthorizationView) AdmissionAccess() exec.RoutineAccess { return v.routineAccess() }

// routineAccessOf is routineAccess over a copy of auth, for callers that hold
// the view by value; call it only when there is something to admit.
func routineAccessOf(auth AuthorizationView) exec.RoutineAccess { return auth.routineAccess() }

// routineAccess is v as routine admission reads it. Env borrows v and stays
// nil unless v has a live engine, which is what RoutineAccess.Live reports.
func (v *AuthorizationView) routineAccess() exec.RoutineAccess {
	access := exec.RoutineAccess{View: v.View}
	if v.engine != nil {
		access.Env = v
	}
	return access
}

// routineAuthorityView is the borrowed local DDL admission authority, or nil.
func routineAuthorityView(c *exec.Context) *AuthorizationView {
	auth, _ := c.RoutineAuthority.(*AuthorizationView)
	return auth
}
