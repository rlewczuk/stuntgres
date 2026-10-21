package fdw

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// GrantKind reports whether a GRANT object kind is a wrapper or server.
func GrantKind(kind string) bool {
	return kind == "foreign server" || kind == "foreign-data wrapper"
}

// GrantChange is the new descriptor of one wrapper or server whose
// ACL a GRANT or REVOKE changes.
type GrantChange struct {
	Address ids.ObjectAddress
	Object  *catalog.ForeignObject
}

// PlanGrant decides GRANT or REVOKE USAGE on foreign-data wrappers or
// servers with the same role closure, grant-source and ACL transformation
// as other GRANTs. pending holds the transaction's uncommitted descriptors;
// the plan reads them and never writes them, and a name repeated in s sees
// the plan's own earlier change. revision allocates each change's
// definition revision. The changes come back in statement order; the
// warnings raised before an error come back with it, to be emitted first.
func PlanGrant(ctx context.Context, view access.View, s *parser.ObjectGrant, pending map[ids.ObjectAddress]catalog.Descriptor, revision func() (ids.DefinitionRevision, error)) ([]GrantChange, []sqlstate.Error, error) {
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(view.Roles, view.State, *s.Grantor)
		if err != nil {
			return nil, nil, err
		}
		if grantor != view.State.CurrentUser {
			return nil, nil, sqlError("0A000", "grantor must be current user")
		}
	}
	for _, privilege := range s.Privileges {
		if _, err := authorization.ForeignPrivilege(privilege, s.Kind); err != nil {
			return nil, nil, err
		}
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, grantee := range s.Grantees {
		if grantee.Public {
			if s.GrantOption {
				return nil, nil, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			role, err := authorization.MembershipRole(view.Roles, view.State, grantee.Role)
			if err != nil {
				return nil, nil, err
			}
			grantees[i].Role = role
		}
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, nil, err
	}
	principals, err := view.Roles.RoleClosure(ctx, actor.Ref, true)
	if err != nil {
		return nil, nil, err
	}
	class := ids.ForeignServerClassID
	if s.Kind == "foreign-data wrapper" {
		class = ids.ForeignWrapperClassID
	}
	var changes []GrantChange
	var warnings []sqlstate.Error
	changed := make(map[ids.ObjectAddress]*catalog.ForeignObject)
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return nil, warnings, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		d, er := Find(view.Catalog, class, name.Parts[0].Text)
		if er != nil {
			return nil, warnings, er
		}
		if previous, ok := changed[d.Address()]; ok {
			d = previous
		} else if previous, ok := pending[d.Address()].(*catalog.ForeignObject); ok {
			d = previous
		}
		source, available, er := authorization.ObjectGrantSource(ctx, d.Owner, d.ACL, authorization.ACLValue{}, principals, actor.Superuser, authorization.UsagePrivilege)
		if er != nil {
			return nil, warnings, er
		}
		if available == 0 && authorization.ACLRights(d.ACL, d.Owner, principals, authorization.UsagePrivilege, false) == 0 {
			return nil, warnings, sqlError("42501", "permission denied for "+s.Kind+" "+d.Name())
		}
		if warning, ok := authorization.GrantWarning(d.Name(), "", authorization.UsagePrivilege, available, false, s.Revoke); ok {
			warnings = append(warnings, warning)
		}
		if available == 0 {
			continue
		}
		baseline := d.ACL
		if !baseline.Explicit {
			// Materialize only ordinary owner USAGE; owner grant options remain implicit.
			baseline = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.UsagePrivilege}}}
		}
		acl, aclChanged, er := authorization.ChangeObjectACL(ctx, view.Roles, baseline, d.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
		if er != nil {
			return nil, warnings, er
		}
		if !aclChanged && d.ACL.Explicit {
			continue
		}
		next := *d
		next.ACL = acl
		next.ObjectRevision, er = revision()
		if er != nil {
			return nil, warnings, er
		}
		if er = access.ValidateACL(view.Roles, &next); er != nil {
			return nil, warnings, er
		}
		changed[d.Address()] = &next
		changes = append(changes, GrantChange{Address: d.Address(), Object: &next})
	}
	return changes, warnings, nil
}
