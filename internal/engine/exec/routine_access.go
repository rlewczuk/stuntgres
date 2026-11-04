package exec

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// RoutineAccess is the authority routine call admission reads: the captured
// access view and the session environment its checks take. Admission checks
// only procedures, and procedure EXECUTE checks never read the environment
// (it supplies the TEMP namespace and type paths), so a readmission that
// switches View.State.CurrentUser keeps the original environment. Env borrows
// live TEMP/path facilities during ordinary admission. A genuine closed
// coordinator capture instead owns immutable catalog/roles with Env nil; its
// private marker permits only environment-independent routine admission.
type RoutineAccess struct {
	access.View
	Env                 access.Environment
	coordinatorCaptured bool // Genuine immutable coordinator snapshot; never a live environment.
}

// Live reports actual engine-backed admission, including a coordinator's
// explicitly detached snapshot. The latter cannot supply a Host or initialize
// execution; procedure ACL/lifetime checks alone need no environment.
func (a RoutineAccess) Live() bool {
	return a.Env != nil || a.coordinatorCaptured && a.Owner != nil && a.Catalog.Owner() == a.Owner
}

// Check checks req with the authority's environment.
func (a RoutineAccess) Check(req access.Requirement) error { return a.View.Check(a.Env, req) }

// Effective returns the current user's rights on ref with the authority's
// environment.
func (a RoutineAccess) Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	return a.View.Effective(a.Env, ref, grantOptions)
}
