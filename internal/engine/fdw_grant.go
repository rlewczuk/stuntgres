package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// The class-specific target adapter uses the same role closure, grant-source,
// ACL transformation and transactional publication authorities as other GRANTs.
func (e *Engine) executeForeignGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	if s.Grantor != nil {
		grantor, er := authorization.MembershipRole(auth.Roles, auth.State, *s.Grantor)
		if er != nil {
			return Result{}, er
		}
		if grantor != auth.State.CurrentUser {
			return Result{}, sqlError("0A000", "grantor must be current user")
		}
	}
	for _, privilege := range s.Privileges {
		if _, err := authorization.ForeignPrivilege(privilege, s.Kind); err != nil {
			return Result{}, err
		}
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, grantee := range s.Grantees {
		if grantee.Public {
			if s.GrantOption {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			role, er := authorization.MembershipRole(auth.Roles, auth.State, grantee.Role)
			if er != nil {
				return Result{}, er
			}
			grantees[i].Role = role
		}
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	principals, err := auth.Roles.RoleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	class := ids.ForeignServerClassID
	if s.Kind == "foreign-data wrapper" {
		class = ids.ForeignWrapperClassID
	}
	changes := scope.owner.Catalog.Clone()
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return Result{}, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		d, er := fdw.Find(auth.Catalog, class, name.Parts[0].Text)
		if er != nil {
			return Result{}, er
		}
		if previous, ok := changes.Descriptors[d.Address()].(*catalog.ForeignObject); ok {
			d = previous
		}
		source, available, er := authorization.ObjectGrantSource(op.Context(), d.Owner, d.ACL, authorization.ACLValue{}, principals, actor.Superuser, authorization.UsagePrivilege)
		if er != nil {
			return Result{}, er
		}
		if available == 0 && authorization.ACLRights(d.ACL, d.Owner, principals, authorization.UsagePrivilege, false) == 0 {
			return Result{}, sqlError("42501", "permission denied for "+s.Kind+" "+d.Name())
		}
		if warning, ok := role.GrantWarning(d.Name(), "", authorization.UsagePrivilege, available, false, s.Revoke); ok {
			op.EmitNotice(session.LevelWarning, warning)
		}
		if available == 0 {
			continue
		}
		baseline := d.ACL
		if !baseline.Explicit {
			// Materialize only ordinary owner USAGE; owner grant options remain implicit.
			baseline = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.UsagePrivilege}}}
		}
		acl, changed, er := authorization.ChangeObjectACL(op.Context(), auth.Roles, baseline, d.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
		if er != nil {
			return Result{}, er
		}
		if !changed && d.ACL.Explicit {
			continue
		}
		next := *d
		next.ACL = acl
		next.ObjectRevision, er = e.catalog.revisionLocked()
		if er != nil {
			return Result{}, er
		}
		if er = access.ValidateACL(auth.Roles, &next); er != nil {
			return Result{}, er
		}
		e.catalogChangeLocked(changes, d.Address(), &next)
	}
	scope.catalog = changes
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	return Result{}, nil
}
