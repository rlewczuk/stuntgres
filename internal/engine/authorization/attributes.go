package authorization

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ValidateRoleAttributes(r Read, state RoleState, stmt parser.Statement) (RoleDescriptor, RoleDescriptor, parser.RoleOptions, error) {
	actor, err := r.Descriptor(state.CurrentUser)
	if err != nil {
		return actor, RoleDescriptor{}, parser.RoleOptions{}, err
	}
	var o parser.RoleOptions
	var target RoleDescriptor
	create, creating := stmt.(*parser.CreateRole)
	if !creating {
		s := stmt.(*parser.AlterRole)
		if s.Identity == "" && strings.HasPrefix(s.Name.Text, "pg_") {
			return actor, target, s.Options, roleDiagnostic("42939", "role name \""+s.Name.Text+"\" is reserved", "Cannot alter reserved roles.")
		}
	}
	if creating {
		o = create.Options
	} else {
		o = stmt.(*parser.AlterRole).Options
	}
	if o.Duplicate != (parser.Position{}) {
		return actor, target, o, insertError("42601", "conflicting or redundant options", o.Duplicate)
	}
	if o.Present&parser.RoleConnectionLimit != 0 && o.ConnectionLimit < -1 {
		return actor, target, o, sqlError("22023", fmt.Sprintf("invalid connection limit: %d", o.ConnectionLimit))
	}
	if creating {
		o = create.Options
		target = RoleDescriptor{Name: create.Name.Text, Login: create.DefaultLogin, Inherit: true, ConnectionLimit: -1}
	} else {
		s := stmt.(*parser.AlterRole)
		o = s.Options
		var ok bool
		if s.Identity != "" {
			ref := state.CurrentUser
			if s.Identity == "session_user" {
				ref = state.SessionUser
			}
			target, err = r.Descriptor(ref)
			ok = err == nil
		} else {
			target, ok = r.Lookup(s.Name.Text)
		}
		if !ok {
			return actor, target, o, sqlError("42704", "role \""+s.Name.Text+"\" does not exist")
		}
	}
	denied := func(detail string) error {
		verb := "alter"
		if creating {
			verb = "create"
		}
		return roleDiagnostic("42501", "permission denied to "+verb+" role", detail)
	}
	if !actor.Superuser {
		if creating {
			if !actor.CreateRole {
				return actor, target, o, denied("Only roles with the CREATEROLE attribute may create roles.")
			}
			if o.Present&parser.RoleSuperuser != 0 && o.Superuser {
				return actor, target, o, denied("Only roles with the SUPERUSER attribute may create roles with the SUPERUSER attribute.")
			}
		} else {
			if target.Superuser {
				return actor, target, o, denied("Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.")
			}
			if o.Present&parser.RoleSuperuser != 0 {
				return actor, target, o, denied("Only roles with the SUPERUSER attribute may change the SUPERUSER attribute.")
			}
			selfPassword := actor.Ref == target.Ref && o.Present == parser.RolePassword
			admin := false
			if !selfPassword && actor.CreateRole {
				admin, err = r.AdminOfRole(actor.Ref, target.Ref)
				if err != nil {
					return actor, target, o, err
				}
			}
			if !selfPassword && !admin {
				detail := "Only roles with the CREATEROLE attribute and the ADMIN option on role \"" + target.Name + "\" may alter this role."
				if o.Present == parser.RolePassword {
					detail = "To change another role's password, the current user must have the CREATEROLE attribute and the ADMIN option on the role."
				}
				return actor, target, o, denied(detail)
			}
		}
		for _, special := range [...]struct {
			field          parser.RoleField
			value, allowed bool
			name           string
		}{
			{parser.RoleCreateDB, o.CreateDB, actor.CreateDB, "CREATEDB"},
			{parser.RoleReplication, o.Replication, actor.Replication, "REPLICATION"},
			{parser.RoleBypassRLS, o.BypassRLS, actor.BypassRLS, "BYPASSRLS"},
		} {
			if o.Present&special.field != 0 && !special.allowed && (!creating || special.value) {
				detail := "Only roles with the " + special.name + " attribute may change the " + special.name + " attribute."
				if creating {
					detail = "Only roles with the " + special.name + " attribute may create roles with the " + special.name + " attribute."
				}
				return actor, target, o, denied(detail)
			}
		}
	}
	if !creating && target.Ref.id == 10 && o.Present&parser.RoleSuperuser != 0 && !o.Superuser {
		return actor, target, o, roleDiagnostic("0A000", "permission denied to alter role", "The bootstrap superuser must have the SUPERUSER attribute.")
	}
	if strings.HasPrefix(target.Name, "pg_") {
		if creating {
			return actor, target, o, roleDiagnostic("42939", "role name \""+target.Name+"\" is reserved", "Role names starting with \"pg_\" are reserved.")
		}
	}
	if creating {
		if target.Name == "public" || target.Name == "none" {
			return actor, target, o, sqlError("42939", "role name \""+target.Name+"\" is reserved")
		}
		if _, ok := r.Lookup(target.Name); ok {
			return actor, target, o, sqlError("42710", "role \""+target.Name+"\" already exists")
		}

	}
	return actor, target, o, nil
}
func ApplyRoleOptions(d RoleDescriptor, o parser.RoleOptions) RoleDescriptor {
	if o.Present&parser.RoleSuperuser != 0 {
		d.Superuser = o.Superuser
	}
	if o.Present&parser.RoleCreateDB != 0 {
		d.CreateDB = o.CreateDB
	}
	if o.Present&parser.RoleCreateRole != 0 {
		d.CreateRole = o.CreateRole
	}
	if o.Present&parser.RoleLogin != 0 {
		d.Login = o.Login
	}
	if o.Present&parser.RoleInherit != 0 {
		d.Inherit = o.Inherit
	}
	if o.Present&parser.RoleReplication != 0 {
		d.Replication = o.Replication
	}
	if o.Present&parser.RoleBypassRLS != 0 {
		d.BypassRLS = o.BypassRLS
	}
	if o.Present&parser.RoleConnectionLimit != 0 {
		d.ConnectionLimit = o.ConnectionLimit
	}
	return d
}

// CreatorSelfGrant selects the optional creator-origin membership.
type CreatorSelfGrant struct {
	Inherit bool
	Set     bool
}

// AttributeCandidate stages descriptor, creator grants and initial clauses together.
func AttributeCandidate(ctx context.Context, r Read, state RoleState, actor, d RoleDescriptor, create *parser.CreateRole, selfGrant CreatorSelfGrant, notices []sqlstate.Error, noticeEligible, warningEligible bool) (Root, []sqlstate.Error, error) {
	n, err := r.state.PutDescriptor(d)
	if err != nil {
		return Root{}, notices, err
	}
	if create == nil {
		return n, notices, nil
	}
	if !actor.Superuser {
		count := len(r.state.edges) + 1
		if selfGrant.Inherit || selfGrant.Set {
			count++
		}
		n.edges = make([]Membership, count)
		copy(n.edges, r.state.edges)
		n.edges[len(r.state.edges)] = Membership{Role: d.Ref, Member: actor.Ref, Grantor: r.state.names["postgres"], Admin: true}
		if selfGrant.Inherit || selfGrant.Set {
			n.edges[len(r.state.edges)+1] = Membership{Role: d.Ref, Member: actor.Ref, Grantor: actor.Ref, Inherit: selfGrant.Inherit, Set: selfGrant.Set}
		}
	}
	candidate := Read{r.owner, &n, r.databaseOwner}
	ownedEdges := !actor.Superuser
	for _, kind := range [...]string{"in", "role", "admin"} {
		for _, option := range create.Options.InitialMemberships {
			if option.Kind != kind {
				continue
			}
			newSpec := parser.RoleSpec{Name: d.Name, Pos: create.Name.Pos}
			mutation := &parser.RoleMembership{Targets: []parser.RoleSpec{newSpec}, Members: option.Members}
			if kind == "in" {
				mutation.Targets = option.Members
				mutation.Members = []parser.RoleSpec{newSpec}
			}
			if kind == "admin" {
				mutation.Options = []parser.MembershipOption{{Name: "admin", Value: true}}
			}
			edges, updated, err := planMembership(ctx, candidate, state, mutation, ownedEdges, notices, noticeEligible, warningEligible)
			notices = updated
			if err != nil {
				return Root{}, notices, err
			}
			n.edges = edges
			ownedEdges = true
		}
	}
	return n, notices, nil
}
