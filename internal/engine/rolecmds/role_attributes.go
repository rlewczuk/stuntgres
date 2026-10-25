package rolecmds

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// validateRoleAttributesLocked is repeated after preparation: held statements,
// catalog changes and effective identity changes never cache authority.
func validateRoleAttributesLocked(e *core.Engine, op *session.Operation, stmt parser.Statement) (core.AuthorizationView, authorization.RoleDescriptor, authorization.RoleDescriptor, parser.RoleOptions, error) {
	v, err := e.AuthorizationLocked(op)
	if err != nil {
		return v, authorization.RoleDescriptor{}, authorization.RoleDescriptor{}, parser.RoleOptions{}, err
	}
	actor, target, o, err := authorization.ValidateRoleAttributes(v.Roles, v.State, stmt)
	if err != nil {
		return v, actor, target, o, err
	}
	if _, creating := stmt.(*parser.CreateRole); creating {
		if _, err := e.RoleStore.PrincipalCandidateLocked(); err != nil {
			return v, actor, target, o, err
		}
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return v, actor, target, o, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return v, actor, target, o, op.Context().Err()
}

// ExecuteRoleAttributes runs CREATE ROLE and ALTER ROLE. The password and
// VALID UNTIL inputs are prepared with no lifecycle lock held; the
// attributes are then revalidated and installed under Engine.Mu.
func ExecuteRoleAttributes(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	_, _, target, o, err := validateRoleAttributesLocked(e, op, stmt)
	typ, _ := e.Catalog.Current().LookupType(sqltype.Timestamptz)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if scope.LockTicket != nil {
		if err := acquireRoleAttributeLocks(scope, stmt, target); err != nil {
			return core.Result{}, err
		}
	}
	// Expensive crypto, random-source I/O and typed input run with no lifecycle
	// locks. Preparation has no catalog effects; all semantics are revalidated.
	var expiry int64
	if o.Present&parser.RoleValidUntil != 0 {
		value, err := typ.InputWithEnvironment(op.Context(), o.ValidUntil, -1, scope.Input)
		if err != nil {
			return core.Result{}, err
		}
		expiry, err = value.Timestamp()
		if err != nil {
			return core.Result{}, err
		}
	}
	var password string
	var notice authorization.PasswordNotice
	if o.Present&parser.RolePassword != 0 && !o.PasswordNull {
		password, notice, err = authorization.PrepareRolePassword(op.Context(), target.Name, o.Password)
		if err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for {
		v, actor, target, o, err := validateRoleAttributesLocked(e, op, stmt)
		if err != nil {
			return core.Result{}, err
		}
		d := authorization.ApplyRoleOptions(target, o)
		d.AuthIDVersion, err = e.RoleStore.RevisionCandidateLocked()
		if err != nil {
			return core.Result{}, err
		}
		if o.Present&parser.RolePassword != 0 {
			d.Password = password
		}
		if o.Present&parser.RoleValidUntil != 0 {
			d.ValidUntil = expiry
			d.HasValidUntil = true
		}
		var create *parser.CreateRole
		if c, creating := stmt.(*parser.CreateRole); creating {
			create = c
			d.Ref, err = e.RoleStore.PrincipalCandidateLocked()
			if err != nil {
				return core.Result{}, err
			}
		}
		n, notices, err := authorization.AttributeCandidate(op.Context(), v.Roles, v.State, actor, d, create, op.Notices, op.NoticeThreshold <= session.LevelNotice, op.NoticeThreshold <= session.LevelWarning)
		if err != nil {
			op.Notices = notices
		}
		if err != nil {
			return core.Result{}, err
		}
		if create != nil && scope.LockTicket != nil {
			e.Mu.Unlock()
			lockErr := acquireInitialMembershipLocks(scope, &v.RoleRecord.Root, &n, d.Ref)
			e.Mu.Lock()
			if lockErr != nil {
				op.Notices = notices
				return core.Result{}, lockErr
			}
			fresh, _, _, _, err := validateRoleAttributesLocked(e, op, stmt)
			if err != nil {
				return core.Result{}, err
			}
			revision, err := e.RoleStore.RevisionCandidateLocked()
			if err != nil {
				return core.Result{}, err
			}
			if fresh.RoleRecord != v.RoleRecord || e.RoleStore.NextPrincipal() != d.Ref.ID() || revision != d.AuthIDVersion {
				continue
			}
		}
		op.Notices = notices
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := core.PublicationOperationLocked(e, op); err != nil {
			return core.Result{}, err
		}
		if _, creating := stmt.(*parser.CreateRole); creating {
			e.RoleStore.InstallPrincipalFrontierLocked(d.Ref.ID() + 1)
		}
		if err := core.InstallOperationRolesLocked(e, op, &access.RoleRecord{Root: n}); err != nil {
			return core.Result{}, err
		}
		role.EmitPasswordNotice(op, notice)
		return core.Result{}, nil
	}
}
