package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// planDatabaseGrant decides GRANT or REVOKE on the current database, named
// database. It returns the new database descriptor, or nil when nothing
// changes, and the statement's warnings, which are set even when an error
// follows them. revision allocates the new descriptor revision.
func planDatabaseGrant(ctx context.Context, view access.View, s *parser.ObjectGrant, database string, revision func() (ids.DefinitionRevision, error)) (*catalog.Database, []sqlstate.Error, error) {
	if s.Kind != "database" || len(s.Relations) == 0 || len(s.Schemas) != 0 {
		return nil, nil, sqlError("0A000", "statement is not supported")
	}
	var requested authorization.Privileges
	all := false
	for _, item := range s.Privileges {
		mask, err := authorization.ObjectPrivilege(item, "database")
		if err != nil {
			return nil, nil, err
		}
		requested |= mask
		all = all || item.All
	}
	for _, name := range s.Relations {
		if len(name.Parts) != 1 {
			return nil, nil, sqlError("0A000", "statement is not supported")
		}
		if name.Parts[0].Text != database {
			return nil, nil, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, name.Parts[0].Text))
		}
	}
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(view.Roles, view.State, *s.Grantor)
		if err != nil {
			return nil, nil, err
		}
		if grantor != view.State.CurrentUser {
			return nil, nil, sqlError("0A000", "grantor must be current user")
		}
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			if s.GrantOption {
				return nil, nil, sqlError("0LP01", "grant options can only be granted to roles")
			}
			grantees[i].Public = true
		} else {
			grantee, err := authorization.MembershipRole(view.Roles, view.State, spec.Role)
			if err != nil {
				return nil, nil, err
			}
			grantees[i].Role = grantee
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
	db := view.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	source, available, err := authorization.ObjectGrantSource(ctx, db.Owner, db.ACL, authorization.ACLValue{}, principals, actor.Superuser, requested)
	if err != nil {
		return nil, nil, err
	}
	if available == 0 && authorization.ACLRights(baseline, db.Owner, principals, authorization.DatabasePrivileges, false) == 0 {
		return nil, nil, sqlError("42501", "permission denied for database "+catalog.DependencyIdentifier(db.Name()))
	}
	var warnings []sqlstate.Error
	if warning, ok := authorization.GrantWarning(db.Name(), "", requested, available, all, s.Revoke); ok {
		warnings = append(warnings, warning)
	}
	if available == 0 {
		return nil, warnings, nil
	}
	acl, changed, err := authorization.ChangeObjectACL(ctx, view.Roles, baseline, db.Owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil || !changed && db.ACL.Explicit {
		return nil, warnings, err
	}
	next := *db
	next.ACL = acl
	next.ObjectRevision, err = revision()
	if err != nil {
		return nil, warnings, err
	}
	if err := access.ValidateACL(view.Roles, &next); err != nil {
		return nil, warnings, err
	}
	return &next, warnings, nil
}
