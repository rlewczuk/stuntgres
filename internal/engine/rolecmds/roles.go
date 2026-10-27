package rolecmds

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteRole runs GRANT and REVOKE of role membership, SET ROLE, SET
// SESSION AUTHORIZATION and DROP ROLE under Engine.Mu. implicit marks a
// statement outside a transaction block, where SET LOCAL ROLE warns.
func ExecuteRole(e *core.Engine, op *session.Operation, stmt parser.Statement, count int, implicit bool) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if selection, ok := stmt.(*parser.RoleSelection); ok && selection.Local && implicit {
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	if err = op.Context().Err(); err != nil {
		return core.Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.RoleMembership:
		return membershipLocked(e, op, v, s)
	case *parser.RoleSelection:
		target, err := role.SetRoleTarget(v.View, s)
		if err != nil {
			return core.Result{}, err
		}
		return core.Result{}, switchSessionRoleLocked(e, op, target, s.Local)
	case *parser.SessionAuthorization:
		target, err := role.SessionAuthorizationTarget(v.View, s)
		if err != nil {
			return core.Result{}, err
		}
		state := v.SessionRoleState
		state.SessionUser, state.CurrentUser = target, target
		state.ActiveRole, state.RoleText = false, ""
		if s.Default && state.ResetRole != (authorization.PrincipalRef{}) {
			if _, err := v.Roles.Descriptor(state.ResetRole); err != nil {
				return core.Result{}, err
			}
			state.CurrentUser, state.ActiveRole, state.RoleText = state.ResetRole, true, state.ResetRoleText
		}
		return core.Result{}, switchSessionRoleLocked(e, op, state, false)
	case *parser.DropRole:
		plan, err := role.PlanDropRoles(RoleHost{core.DDLHost{E: e, Op: op}}, v, s)
		if err != nil {
			return core.Result{}, err
		}
		return publishDroppedRolesLocked(e, op, plan)
	}
	return core.Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

// switchSessionRoleLocked installs the complete decided physical identity and
// journals the role GUC before publication. Engine.Mu is held; lock order is
// Settings.Mu then Session.Mu.
func switchSessionRoleLocked(e *core.Engine, op *session.Operation, target authorization.RoleState, local bool) error {
	sess := op.Session
	settings := sess.Settings
	settings.Mu.Lock()
	defer settings.Mu.Unlock()
	sess.Mu.Lock()
	defer sess.Mu.Unlock()
	a := sess.Authorization
	// Reserve failure unwind, retained savepoints and outer restoration.
	if err := session.RoleRevisionHeadroom(a, uint64(len(sess.Transactions.Savepoints))+3); err != nil {
		return err
	}
	if err := core.PublicationOperationLocked(e, op); err != nil {
		return err
	}
	touchOperationIdentityLocked(op)
	settings.TouchRole(a.State, local)
	a.State = target
	a.Revision++
	return nil
}

// publishDroppedRolesLocked installs a decided DROP ROLE: the role record
// joins the operation, and a retained transaction remembers the retired roles.
func publishDroppedRolesLocked(e *core.Engine, op *session.Operation, plan role.DropRolesPlan) (core.Result, error) {
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := core.PublicationOperationLocked(e, op); err != nil {
		return core.Result{}, err
	}
	if err := core.InstallOperationRolesLocked(e, op, plan.Roles); err != nil {
		return core.Result{}, err
	}
	scope := core.ActiveScopeOfLease(op.Lease)
	if t := op.Session.Transactions; t.State == session.TxActive || t.RetainsTransaction() || scope != nil && scope.Catalog != nil {
		// The installer cloned this exact operation journal; savepoint roots
		// never share its mutable retirement set.
		c := core.OperationCatalog(op)
		if c.RetiredRoles == nil {
			c.RetiredRoles = make(map[authorization.PrincipalRef]struct{}, len(plan.Retired))
		}
		for _, ref := range plan.Retired {
			c.RetiredRoles[ref] = struct{}{}
		}
	}
	return core.Result{}, nil
}
