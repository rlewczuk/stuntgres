package role

import (
	"fmt"
	"sort"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

// SetRoleTarget returns the role SET ROLE or RESET ROLE switches to, after
// checking that the session user may switch to it.
func SetRoleTarget(view access.View, s *parser.RoleSelection) (authorization.PrincipalRef, error) {
	target := view.State.SessionUser
	if s.Reset && view.State.ResetRole != (authorization.PrincipalRef{}) {
		target = view.State.ResetRole
	} else if !s.Reset && s.Name != "none" {
		d, ok := view.Roles.Lookup(s.Name)
		if !ok {
			return authorization.PrincipalRef{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
		}
		target = d.Ref
	}
	if s.Reset {
		if _, err := view.Roles.Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return target, nil
	}
	allowed := target == view.State.SessionUser
	if !allowed {
		var err error
		allowed, err = view.Roles.CanSetRole(view.State.SessionUser, target)
		if err != nil {
			return authorization.PrincipalRef{}, err
		}
	}
	if !allowed {
		return authorization.PrincipalRef{}, sqlError("42501", fmt.Sprintf("permission denied to set role \"%s\"", s.Name))
	}
	return target, nil
}

// SessionAuthorizationTarget returns the role SET SESSION AUTHORIZATION
// switches to, after checking that the authenticated role may.
func SessionAuthorizationTarget(view access.View, s *parser.SessionAuthorization) (authorization.PrincipalRef, error) {
	target := view.State.Authenticated
	if !s.Default {
		d, ok := view.Roles.Lookup(s.Name)
		if !ok {
			return authorization.PrincipalRef{}, sqlError("22023", fmt.Sprintf("role \"%s\" does not exist", s.Name))
		}
		target = d.Ref
	}
	authenticated, _ := view.Roles.Descriptor(view.State.Authenticated)
	if !authenticated.Superuser && target != authenticated.Ref {
		return authorization.PrincipalRef{}, sqlError("42501", fmt.Sprintf("permission denied to set session authorization \"%s\"", s.Name))
	}
	return target, nil
}

// DropRolesPlan is a decided DROP ROLE: the role record with the roles
// retired, and the retired roles in statement order.
type DropRolesPlan struct {
	Roles   *access.RoleRecord
	Retired []authorization.PrincipalRef
}

// PlanDropRoles decides DROP ROLE: the superuser gate, missing, current and
// session roles, predefined roles and every remaining dependency, in
// PostgreSQL's order.
func PlanDropRoles(h Host, auth ddl.Authority, s *parser.DropRole) (DropRolesPlan, error) {
	view := h.AccessView(auth)
	actor, exists := view.Roles.Find(view.State.CurrentUser)
	if !exists || !actor.Superuser {
		return DropRolesPlan{}, Diagnostic("42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
	}
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor, len(s.Names))
	ordered := make([]authorization.PrincipalRef, 0, len(s.Names))
	for _, name := range s.Names {
		d, ok := view.Roles.Lookup(name)
		if !ok {
			return DropRolesPlan{}, sqlError("42704", fmt.Sprintf("role \"%s\" does not exist", name))
		}
		if d.Ref == view.State.CurrentUser {
			return DropRolesPlan{}, sqlError("55006", "current user cannot be dropped")
		}
		if d.Ref == view.State.SessionUser {
			return DropRolesPlan{}, sqlError("55006", "session user cannot be dropped")
		}
		if _, ok := targets[d.Ref]; !ok {
			ordered = append(ordered, d.Ref)
			targets[d.Ref] = d
		}
	}
	// Hypothetical endpoint cleanup precedes every dependency check. A grantor
	// alone is not an endpoint and therefore retains its membership dependency.
	retained := view.RoleRecord.Root.RetainedMemberships(targets)
	for _, ref := range ordered {
		d := targets[ref]
		if d.Predefined() {
			return DropRolesPlan{}, Diagnostic("2BP01", "cannot drop role "+d.Name+" because it is required by the database system", "")
		}
		var dependencies []access.RoleDependency
		for _, object := range view.Catalog.Descriptors() {
			access.DescriptorRoleDependencies(object, ref, &dependencies)
		}
		access.DefaultRoleDependencies(view.Catalog, view.Roles, ref, &dependencies)
		dependencies = append(dependencies, h.OtherDatabaseDependencies(ref)...)
		for edge := range retained {
			if edge.Grantor == ref {
				member, _ := view.Roles.Descriptor(edge.Member)
				granted, _ := view.Roles.Descriptor(edge.Role)
				dependencies = append(dependencies, access.RoleDependency{Kind: 'a', Description: "privileges for membership of role " + member.Name + " in role " + granted.Name})
			}
		}
		for _, feature := range h.ReferencingFeatures(ref) {
			dependencies = append(dependencies, access.RoleDependency{Kind: 'a', Description: "privileges for " + feature})
		}
		if len(dependencies) > 0 {
			sort.Slice(dependencies, func(i, j int) bool {
				a, b := dependencies[i], dependencies[j]
				if a.Address.ObjectID != b.Address.ObjectID {
					return a.Address.ObjectID < b.Address.ObjectID
				}
				if a.Address.ClassID != b.Address.ClassID {
					return a.Address.ClassID < b.Address.ClassID
				}
				if a.Address.SubID != b.Address.SubID {
					return uint32(a.Address.SubID) < uint32(b.Address.SubID)
				}
				if a.Kind != b.Kind {
					return a.Kind < b.Kind
				}
				return a.Description < b.Description
			})
			details := make([]string, len(dependencies))
			for i, dep := range dependencies {
				details[i] = h.DescribeRoleDependency(auth, dep)
			}
			return DropRolesPlan{}, Diagnostic("2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", d.Name), strings.Join(details, "\n"))
		}
	}
	if view.RoleRecord.Revision == ^uint64(0) {
		return DropRolesPlan{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	retired := view.RoleRecord.Root.Retire(targets)
	return DropRolesPlan{Roles: &access.RoleRecord{Root: retired, Revision: view.RoleRecord.Revision + 1}, Retired: ordered}, nil
}
