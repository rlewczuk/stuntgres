package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func executeRole(e *Engine, op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if selection, ok := stmt.(*parser.RoleSelection); ok && selection.Local && implicit {
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if err = op.Context().Err(); err != nil {
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.RoleMembership:
		return membershipLocked(e, op, v, s)
	case *parser.RoleSelection:
		target, err := role.SetRoleTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		return Result{}, switchSessionRoleLocked(e, op, target, s.Local, false)
	case *parser.SessionAuthorization:
		target, err := role.SessionAuthorizationTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		return Result{}, switchSessionRoleLocked(e, op, target, false, true)
	case *parser.DropRole:
		plan, err := role.PlanDropRoles(RoleHost{DDLHost{E: e, Op: op}}, v, s)
		if err != nil {
			return Result{}, err
		}
		return publishDroppedRolesLocked(e, op, plan)
	}
	return Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

// switchSessionRoleLocked makes target op's current user, and its session
// user too when sessionUser is set (SET SESSION AUTHORIZATION). local marks a
// SET LOCAL ROLE for the role setting's restore point. The caller holds
// Engine.mu; this takes Settings.Mu, then Session.Mu, and releases both
// before it returns.
func switchSessionRoleLocked(e *Engine, op *session.Operation, target authorization.PrincipalRef, local, sessionUser bool) error {
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
	if err := PublicationOperationLocked(e, op); err != nil {
		return err
	}
	touchOperationIdentityLocked(op)
	settings.TouchRole(a.State.CurrentUser, local)
	if sessionUser {
		a.State.SessionUser = target
	}
	a.State.CurrentUser = target
	a.Revision++
	return nil
}

// publishDroppedRolesLocked installs a decided DROP ROLE: the role record
// joins the operation, and a retained transaction remembers the retired roles.
func publishDroppedRolesLocked(e *Engine, op *session.Operation, plan role.DropRolesPlan) (Result, error) {
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if err := installOperationRolesLocked(e, op, plan.Roles); err != nil {
		return Result{}, err
	}
	if t := op.Session.Transactions; t.State == session.TxActive || t.RetainsTransaction() {
		// installOperationRolesLocked cloned the same catalog owner; no saved
		// candidate shares this mutable set. Standalone publication needs none.
		if t.Catalog.RetiredRoles == nil {
			t.Catalog.RetiredRoles = make(map[authorization.PrincipalRef]struct{}, len(plan.Retired))
		}
		for _, ref := range plan.Retired {
			t.Catalog.RetiredRoles[ref] = struct{}{}
		}
	}
	return Result{}, nil
}
