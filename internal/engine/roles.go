package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The role default is an internal physical-session hook, not a registered GUC
// or a public connector option. Providers run without publication locks.
func (e *Engine) initialRole(ctx context.Context, authenticated authorization.PrincipalRef, provider settings.SessionDefaultProvider) (authorization.PrincipalRef, error) {
	if provider == nil {
		return authorization.PrincipalRef{}, nil
	}
	name, present, err := provider.SessionDefault(ctx, settings.Definition{Name: "role", BootDefault: "none", Type: settings.String})
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return authorization.PrincipalRef{}, err
	}
	if !present || name == "none" {
		return authorization.PrincipalRef{}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
	target, ok := roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", name))
	}
	if err := authorization.ValidateInitialRole(roles, authenticated, target.Ref); err != nil {
		return authorization.PrincipalRef{}, err
	}
	return target.Ref, nil
}

func (e *Engine) executeRole(op *session.Operation, stmt parser.Statement, count int, implicit bool) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if selection, ok := stmt.(*parser.RoleSelection); ok && selection.Local && implicit {
		op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if err = op.Context().Err(); err != nil {
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.ObjectGrant:
		return e.objectGrantLocked(op, v, s)
	case *parser.AlterDefaultPrivileges:
		return e.alterDefaultPrivilegesLocked(op, v, s)
	case *parser.RoleMembership:
		return e.membershipLocked(op, v, s)
	case *parser.RoleSelection:
		target, err := role.SetRoleTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		return Result{}, e.switchSessionRoleLocked(op, target, s.Local, false)
	case *parser.SessionAuthorization:
		target, err := role.SessionAuthorizationTarget(v.View, s)
		if err != nil {
			return Result{}, err
		}
		return Result{}, e.switchSessionRoleLocked(op, target, false, true)
	case *parser.DropRole:
		plan, err := role.PlanDropRoles(ddlHost{e: e, op: op}, v, s)
		if err != nil {
			return Result{}, err
		}
		return e.publishDroppedRolesLocked(op, plan)
	}
	return Result{}, fmt.Errorf("stuntgres: invalid role statement")
}

// switchSessionRoleLocked makes target op's current user, and its session
// user too when sessionUser is set (SET SESSION AUTHORIZATION). local marks a
// SET LOCAL ROLE for the role setting's restore point. The caller holds
// Engine.mu; this takes Settings.Mu, then Session.Mu, and releases both
// before it returns.
func (e *Engine) switchSessionRoleLocked(op *session.Operation, target authorization.PrincipalRef, local, sessionUser bool) error {
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
	if err := publicationOperationLocked(e, op); err != nil {
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
func (e *Engine) publishDroppedRolesLocked(op *session.Operation, plan role.DropRolesPlan) (Result, error) {
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if err := e.installOperationRolesLocked(op, plan.Roles); err != nil {
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
