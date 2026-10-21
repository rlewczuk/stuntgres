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
// the engine's authorization view and is never retained past admission; it
// is nil for an authority without a live engine.
type RoutineAccess struct {
	access.View
	Env access.Environment
}

// Live reports whether the authority belongs to a live engine.
func (a RoutineAccess) Live() bool { return a.Env != nil }

// Check checks req with the authority's environment.
func (a RoutineAccess) Check(req access.Requirement) error { return a.View.Check(a.Env, req) }

// Effective returns the current user's rights on ref with the authority's
// environment.
func (a RoutineAccess) Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	return a.View.Effective(a.Env, ref, grantOptions)
}
