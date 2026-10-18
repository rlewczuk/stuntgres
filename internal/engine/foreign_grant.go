package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func foreignGrantKind(kind string) bool {
	return kind == "foreign server" || kind == "foreign-data wrapper"
}

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
		grantor, er := authorization.MembershipRole(auth.roles, auth.state, *s.Grantor)
		if er != nil {
			return Result{}, er
		}
		if grantor != auth.state.CurrentUser {
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
			role, er := authorization.MembershipRole(auth.roles, auth.state, grantee.Role)
			if er != nil {
				return Result{}, er
			}
			grantees[i].Role = role
		}
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	principals, err := auth.roles.RoleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	class := ids.ForeignServerClassID
	if s.Kind == "foreign-data wrapper" {
		class = ids.ForeignWrapperClassID
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return Result{}, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		d, er := findForeign(auth.catalog, class, name.Parts[0].Text)
		if er != nil {
			return Result{}, er
		}
		if previous, ok := changes.descriptors[d.address].(*foreignDescriptor); ok {
			d = previous
		}
		source, available, er := authorization.ObjectGrantSource(op.Context(), d.owner, d.acl, authorization.ACLValue{}, principals, actor.Superuser, authorization.UsagePrivilege)
		if er != nil {
			return Result{}, er
		}
		if available == 0 && authorization.ACLRights(d.acl, d.owner, principals, authorization.UsagePrivilege, false) == 0 {
			return Result{}, sqlError("42501", "permission denied for "+s.Kind+" "+d.name)
		}
		grantWarning(op, d.name, "", authorization.UsagePrivilege, available, false, s.Revoke)
		if available == 0 {
			continue
		}
		baseline := d.acl
		if !baseline.Explicit {
			// Materialize only ordinary owner USAGE; owner grant options remain implicit.
			baseline = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.owner, Grantee: authorization.ACLGrantee{Role: d.owner}, Privileges: authorization.UsagePrivilege}}}
		}
		acl, changed, er := authorization.ChangeObjectACL(op.Context(), auth.roles, baseline, d.owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
		if er != nil {
			return Result{}, er
		}
		if !changed && d.acl.Explicit {
			continue
		}
		next := *d
		next.acl = acl
		next.revision, er = e.revisionLocked()
		if er != nil {
			return Result{}, er
		}
		if er = validateACL(auth.roles, &next); er != nil {
			return Result{}, er
		}
		e.catalogChangeLocked(changes, d.address, &next)
	}
	scope.catalog = changes
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	return Result{}, nil
}
