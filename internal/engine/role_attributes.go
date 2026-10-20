package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// validateRoleAttributesLocked is repeated after preparation: held statements,
// catalog changes and effective identity changes never cache authority.
func (e *Engine) validateRoleAttributesLocked(op *Operation, stmt parser.Statement) (AuthorizationView, authorization.RoleDescriptor, authorization.RoleDescriptor, parser.RoleOptions, error) {
	v, err := e.authorizationLocked(op)
	if err != nil {
		return v, authorization.RoleDescriptor{}, authorization.RoleDescriptor{}, parser.RoleOptions{}, err
	}
	actor, target, o, err := authorization.ValidateRoleAttributes(v.Roles, v.State, stmt)
	if err != nil {
		return v, actor, target, o, err
	}
	if _, creating := stmt.(*parser.CreateRole); creating {
		if _, err := e.roles.principalCandidateLocked(); err != nil {
			return v, actor, target, o, err
		}
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return v, actor, target, o, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return v, actor, target, o, op.Context().Err()
}

func (e *Engine) executeRoleAttributes(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	e.mu.Lock()
	_, _, target, o, err := e.validateRoleAttributesLocked(op, stmt)
	typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Timestamptz)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	// Expensive crypto, random-source I/O and typed input run with no lifecycle
	// locks. Preparation has no catalog effects; all semantics are revalidated.
	var expiry int64
	if o.Present&parser.RoleValidUntil != 0 {
		value, err := typ.InputWithEnvironment(op.Context(), o.ValidUntil, -1, scope.input)
		if err != nil {
			return Result{}, err
		}
		expiry, err = value.Timestamp()
		if err != nil {
			return Result{}, err
		}
	}
	var password string
	var notice authorization.PasswordNotice
	if o.Present&parser.RolePassword != 0 && !o.PasswordNull {
		password, notice, err = authorization.PrepareRolePassword(op.Context(), target.Name, o.Password)
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	v, actor, target, o, err := e.validateRoleAttributesLocked(op, stmt)
	if err != nil {
		return Result{}, err
	}
	d := authorization.ApplyRoleOptions(target, o)
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
		d.Ref, err = e.roles.principalCandidateLocked()
		if err != nil {
			return Result{}, err
		}
	}
	n, notices, err := authorization.AttributeCandidate(op.Context(), v.Roles, v.State, actor, d, create, op.Notices, op.NoticeThreshold <= session.LevelNotice, op.NoticeThreshold <= session.LevelWarning)
	op.Notices = notices
	if err != nil {
		return Result{}, err
	}
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if _, creating := stmt.(*parser.CreateRole); creating {
		e.roles.nextPrincipal = d.Ref.ID() + 1
	}
	e.installOperationRolesLocked(op, &access.RoleRecord{Root: n, Revision: v.RoleRecord.Revision + 1})
	emitPasswordNotice(op, notice)
	return Result{}, nil
}
