package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) executeDatabaseGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if s.Kind != "database" || len(s.Relations) == 0 || len(s.Schemas) != 0 {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	var requested authorization.Privileges
	all := false
	for _, item := range s.Privileges {
		mask, err := authorization.ObjectPrivilege(item, "database")
		if err != nil {
			return Result{}, err
		}
		requested |= mask
		all = all || item.All
	}
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return Result{}, sqlError("0A000", "statement is not supported")
		}
		if name.Parts[0].Text != e.databaseName {
			return Result{}, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, name.Parts[0].Text))
		}
	}
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(v.Roles, v.State, *s.Grantor)
		if err != nil {
			return Result{}, err
		}
		if grantor != v.State.CurrentUser {
			return Result{}, sqlError("0A000", "grantor must be current user")
		}
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			if s.GrantOption {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			role, err := authorization.MembershipRole(v.Roles, v.State, spec.Role)
			if err != nil {
				return Result{}, err
			}
			grantees[i].Role = role
		}
	}
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	principals, err := v.Roles.RoleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	db := v.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	source, available, err := authorization.ObjectGrantSource(op.Context(), db.Owner, db.ACL, authorization.ACLValue{}, principals, actor.Superuser, requested)
	if err != nil {
		return Result{}, err
	}
	if available == 0 && authorization.ACLRights(baseline, db.Owner, principals, authorization.DatabasePrivileges, false) == 0 {
		return Result{}, sqlError("42501", "permission denied for database "+catalog.DependencyIdentifier(db.Name()))
	}
	if warning, ok := role.GrantWarning(db.Name(), "", requested, available, all, s.Revoke); ok {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if available == 0 {
		return Result{}, nil
	}
	acl, changed, err := authorization.ChangeObjectACL(op.Context(), v.Roles, baseline, db.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil || !changed && db.ACL.Explicit {
		return Result{}, err
	}
	next := *db
	next.ACL = acl
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	if err := access.ValidateACL(v.Roles, &next); err != nil {
		return Result{}, err
	}
	changes := scope.owner.Catalog.Clone()
	e.catalogChangeLocked(changes, db.Address(), &next)
	scope.catalog = changes
	scope.catalogAuthority = v.catalogAuthorityLocked(ids.ObjectRef{Address: db.Address()}, e.catalog.state.Get(db.Address()))
	return Result{}, nil
}
