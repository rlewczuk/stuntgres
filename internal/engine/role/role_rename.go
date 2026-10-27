package role

import (
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

// PlanRoleRename resolves and authorizes a rename against one captured view.
// The descriptor keeps every identity-bearing field; the executor stamps its
// tuple version only after lock readmission and publishes the complete record.
func PlanRoleRename(v access.View, s *parser.RenameRole) (authorization.RoleDescriptor, bool, error) {
	ref, err := authorization.MembershipRole(v.Roles, v.State, s.Role)
	if err != nil {
		return authorization.RoleDescriptor{}, false, err
	}
	d, err := v.Roles.Descriptor(ref)
	if err != nil {
		return d, false, err
	}
	if ref == v.State.SessionUser {
		return d, false, Diagnostic("0A000", "session user cannot be renamed", "")
	}
	if ref == v.State.CurrentUser {
		return d, false, Diagnostic("0A000", "current user cannot be renamed", "")
	}
	if strings.HasPrefix(d.Name, "pg_") {
		return d, false, Diagnostic("42939", "role name \""+d.Name+"\" is reserved", "Role names starting with \"pg_\" are reserved.")
	}
	if strings.HasPrefix(s.Name.Text, "pg_") {
		return d, false, Diagnostic("42939", "role name \""+s.Name.Text+"\" is reserved", "Role names starting with \"pg_\" are reserved.")
	}
	if _, exists := v.Roles.Lookup(s.Name.Text); exists {
		return d, false, Diagnostic("42710", "role \""+s.Name.Text+"\" already exists", "")
	}
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return d, false, err
	}
	if !actor.Superuser {
		if d.Superuser {
			return d, false, Diagnostic("42501", "permission denied to rename role", "Only roles with the SUPERUSER attribute may rename roles with the SUPERUSER attribute.")
		}
		admin := false
		if actor.CreateRole {
			admin, err = v.Roles.AdminOfRole(actor.Ref, d.Ref)
			if err != nil {
				return d, false, err
			}
		}
		if !admin {
			return d, false, Diagnostic("42501", "permission denied to rename role", "Only roles with the CREATEROLE attribute and the ADMIN option on role \""+d.Name+"\" may rename this role.")
		}
	}
	d.Name = s.Name.Text
	cleared := strings.HasPrefix(d.Password, "md5")
	if cleared {
		d.Password = ""
	}
	return d, cleared, nil
}
