package role

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// RoleSettingsTarget is an execution-time scope, never retained prepared authority.
// Database is the actual named descriptor, including for noncurrent databases.
type RoleSettingsTarget struct {
	Key      access.RoleSettingKey
	Database *catalog.Database
}

// PlanRoleSettingsTarget resolves and authorizes the role before the database,
// matching ALTER ROLE's diagnostic order. It neither locks nor publishes.
func PlanRoleSettingsTarget(view access.View, s *parser.AlterRoleSettings) (RoleSettingsTarget, error) {
	var target RoleSettingsTarget
	if !s.All {
		ref, err := authorization.MembershipRole(view.Roles, view.State, s.Role)
		if err != nil {
			return target, err
		}
		target.Key.Role = ref
		actor, err := view.Roles.Descriptor(view.State.CurrentUser)
		if err != nil {
			return target, err
		}
		role, err := view.Roles.Descriptor(ref)
		if err != nil {
			return target, err
		}
		if !actor.Superuser {
			if role.Superuser {
				return target, Diagnostic("42501", "permission denied to alter role", "Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.")
			}
			if actor.Ref != ref {
				admin := false
				if actor.CreateRole {
					admin, err = view.Roles.AdminOfRole(actor.Ref, ref)
					if err != nil {
						return target, err
					}
				}
				if !admin {
					return target, Diagnostic("42501", "permission denied to alter role", fmt.Sprintf("Only roles with the CREATEROLE attribute and the ADMIN option on role \"%s\" may alter this role.", role.Name))
				}
			}
		}
	}
	if s.Database != nil {
		address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: s.Database.Text})
		db, ok := view.Catalog.Database(address)
		if !ok {
			return target, sqlError("3D000", fmt.Sprintf("database \"%s\" does not exist", s.Database.Text))
		}
		target.Database, target.Key.Database = db, address
	}
	if s.All {
		actor, err := view.Roles.Descriptor(view.State.CurrentUser)
		if err != nil {
			return target, err
		}
		if !actor.Superuser {
			if target.Database == nil {
				return target, Diagnostic("42501", "permission denied to alter setting", "Only roles with the SUPERUSER attribute may alter settings globally.")
			}
			allowed, err := view.Roles.InheritsRole(actor.Ref, target.Database.Owner)
			if err != nil {
				return target, err
			}
			if !allowed {
				return target, sqlError("42501", "must be owner of database "+catalog.DependencyIdentifier(target.Database.Name()))
			}
		}
	}
	return target, nil
}
