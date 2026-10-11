package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// validateRoleAttributesLocked is repeated after preparation: held statements,
// catalog changes and effective identity changes never cache authority.
func (e *Engine) validateRoleAttributesLocked(op *Operation, stmt parser.Statement) (RoleDescriptor, RoleDescriptor, parser.RoleOptions, error) {
	v, err := e.authorizationLocked(op)
	if err != nil {
		return RoleDescriptor{}, RoleDescriptor{}, parser.RoleOptions{}, err
	}
	actor, err := v.roles.Descriptor(v.state.CurrentUser)
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
			ref := v.state.CurrentUser
			if s.Identity == "session_user" {
				ref = v.state.SessionUser
			}
			target, err = v.roles.Descriptor(ref)
			ok = err == nil
		} else {
			target, ok = v.roles.Lookup(s.Name.Text)
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
				admin, err = v.roles.adminOfRole(actor.Ref, target.Ref)
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
		if _, ok := v.roles.Lookup(target.Name); ok {
			return actor, target, o, sqlError("42710", "role \""+target.Name+"\" already exists")
		}
		if _, err := e.nextRolePrincipalLocked(); err != nil {
			return actor, target, o, err
		}
	}
	if e.roles.revision == ^uint64(0) {
		return actor, target, o, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	return actor, target, o, op.Context().Err()
}

func applyRoleOptions(d RoleDescriptor, o parser.RoleOptions) RoleDescriptor {
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

func (e *Engine) executeRoleAttributes(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	e.mu.Lock()
	_, target, o, err := e.validateRoleAttributesLocked(op, stmt)
	typ, _ := (CatalogRead{e, e.state}).LookupType(sqltype.Timestamptz)
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
	var notice passwordNotice
	if o.Present&parser.RolePassword != 0 && !o.PasswordNull {
		password, notice, err = prepareRolePassword(op.Context(), target.Name, o.Password)
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	actor, target, o, err := e.validateRoleAttributesLocked(op, stmt)
	if err != nil {
		return Result{}, err
	}
	d := applyRoleOptions(target, o)
	if o.Present&parser.RolePassword != 0 {
		d.Password = password
	}
	if o.Present&parser.RoleValidUntil != 0 {
		d.ValidUntil = expiry
		d.HasValidUntil = true
	}
	n := cloneRoles(e.roles)
	if _, creating := stmt.(*parser.CreateRole); creating {
		d.Ref, err = e.nextRolePrincipalLocked()
		if err != nil {
			return Result{}, err
		}
		n.names[d.Name] = d.Ref
		if !actor.Superuser {
			n.edges = make([]Membership, len(e.roles.edges)+1)
			copy(n.edges, e.roles.edges)
			n.edges[len(e.roles.edges)] = Membership{Role: d.Ref, Member: actor.Ref, Grantor: e.roles.names["postgres"], Admin: true}
		}
	}
	n.roles[d.Ref] = d
	if create, creating := stmt.(*parser.CreateRole); creating && len(create.Options.InitialMemberships) != 0 {
		v, err := e.authorizationLocked(op)
		if err != nil {
			return Result{}, err
		}
		candidate := RoleRead{e, n}
		ownedEdges := !actor.Superuser
		// Preserve PostgreSQL's category order and stage every initial clause
		// in the same private root as attributes and the creator ADMIN grant.
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
				edges, err := planMembership(op, candidate, v.state, mutation, ownedEdges)
				if err != nil {
					return Result{}, err
				}
				n.edges = edges
				ownedEdges = true
			}
		}
	}
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if _, creating := stmt.(*parser.CreateRole); creating {
		e.nextPrincipal = d.Ref.id + 1
	}
	e.roles = n
	notice.emit(op)
	return Result{}, nil
}
