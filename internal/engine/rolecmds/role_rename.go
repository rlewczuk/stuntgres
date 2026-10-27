package rolecmds

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteRoleRename takes the cluster tuple and destination-name writers outside
// Engine.Mu. Every wait is followed by fresh authority and tuple readmission;
// a same-name replacement is never adopted as the original source principal.
func ExecuteRoleRename(e *core.Engine, scope *core.StatementScope, stmt *parser.RenameRole, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	v, err := e.AuthorizationLocked(op)
	var captured authorization.RoleDescriptor
	if err == nil {
		captured, _, err = role.PlanRoleRename(v.View, stmt)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if scope.LockTicket != nil {
		if err := scope.AcquireRolePrincipal(captured.Ref, lock.AccessExclusive, true); err != nil {
			return core.Result{}, err
		}
		if err := readmitRoleRename(scope, stmt, captured); err != nil {
			return core.Result{}, err
		}
		resource := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.RoleClassID, Name: stmt.Name.Text}}
		if err := scope.AcquireRoleResource(resource, lock.AccessExclusive); err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err = e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	if err := checkRoleTupleUnchanged(v.Roles, captured); err != nil {
		return core.Result{}, err
	}
	d, cleared, err := role.PlanRoleRename(v.View, stmt)
	if err != nil {
		return core.Result{}, err
	}
	d.AuthIDVersion, err = e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return core.Result{}, err
	}
	next, err := v.RoleRecord.Root.PutDescriptor(d)
	if err != nil {
		return core.Result{}, err
	}
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := core.PublicationOperationLocked(e, op); err != nil {
		return core.Result{}, err
	}
	if err := core.InstallOperationRolesLocked(e, op, v.RoleRecord.WithRoot(next)); err != nil {
		return core.Result{}, err
	}
	if cleared {
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "MD5 password cleared because of role rename"})
	}
	return core.Result{}, nil
}

func readmitRoleRename(scope *core.StatementScope, stmt *parser.RenameRole, captured authorization.RoleDescriptor) error {
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	if err := checkRoleTupleUnchanged(v.Roles, captured); err != nil {
		return err
	}
	_, _, err = role.PlanRoleRename(v.View, stmt)
	return err
}
