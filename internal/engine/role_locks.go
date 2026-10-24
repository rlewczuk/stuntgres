package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Every shared role resource uses the cluster's genuine stable lock domain;
// the transaction ticket remains attached to its own database and catalog epoch.
func (s *StatementScope) acquireRoleResource(resource lock.Resource, mode lock.Mode) error {
	e := s.engine()
	e.mu.Lock()
	if resource.Domain() != &e.cluster.roleDomain || resource.Epoch != 1 || s.txRef.Epoch() != e.catalog.State.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	err := e.assignTransactionXIDLocked(s.txRef)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.acquireTransactionResource(resource, mode); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.txRef.Epoch() != e.catalog.State.Epoch() || s.owner.Ref != s.txRef {
		return ids.ErrCatalogReplaced
	}
	return s.op.Context().Err()
}

func (s *StatementScope) acquireRolePrincipal(ref authorization.PrincipalRef, mode lock.Mode, tuple bool) error {
	e := s.engine()
	if ref.ID() == 0 || ref.ID() > math.MaxUint32 || ref != e.roles.Domain.Principal(ref.ID()) {
		return fmt.Errorf("stuntgres: foreign role lock principal")
	}
	resource := lock.Resource{ObjectRef: e.cluster.roleDomain.Ref(1, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: ids.OID(ref.ID())}), RoleTuple: tuple}
	return s.acquireRoleResource(resource, mode)
}

func (s *StatementScope) roleStillPresent(ref authorization.PrincipalRef) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	if _, ok := v.Roles.Find(ref); !ok {
		return sqlError("42704", fmt.Sprintf("role %d was concurrently dropped", ref.ID()))
	}
	return nil
}

func (s *StatementScope) roleTupleUnchanged(captured authorization.RoleDescriptor) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	return checkRoleTupleUnchanged(v.Roles, captured)
}

func checkRoleTupleUnchanged(roles authorization.Read, captured authorization.RoleDescriptor) error {
	actual, exists := roles.Find(captured.Ref)
	if !exists {
		return sqlError("XX000", "tuple concurrently deleted")
	}
	if actual.AuthIDVersion != captured.AuthIDVersion {
		return sqlError("XX000", "tuple concurrently updated")
	}
	return nil
}

func (s *StatementScope) acquireRoleAttributeLocks(stmt parser.Statement, target authorization.RoleDescriptor) error {
	if create, ok := stmt.(*parser.CreateRole); ok {
		e := s.engine()
		resource := lock.Resource{ObjectRef: e.cluster.roleDomain.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.RoleClassID, Name: create.Name.Text}}
		if err := s.acquireRoleResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.mu.Lock()
		v, err := e.authorizationLocked(s.op)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		_, exists := v.Roles.Lookup(create.Name.Text)
		e.mu.Unlock()
		if exists {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_authid_rolname_index"`, Detail: "Key (rolname)=(" + create.Name.Text + ") already exists.", Schema: "pg_catalog", Table: "pg_authid", Constraint: "pg_authid_rolname_index"}
		}
		return nil
	}
	if err := s.acquireRolePrincipal(target.Ref, lock.AccessExclusive, true); err != nil {
		return err
	}
	return s.roleTupleUnchanged(target)
}

func (s *StatementScope) acquireRoleRetirementLocks(statement *parser.DropRole) error {
	if s.owner.State != session.TxActive && s.owner.RetainsTransaction() {
		return nil
	}
	for _, name := range statement.Names {
		e := s.engine()
		e.mu.Lock()
		v, err := e.authorizationLocked(s.op)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		err = role.CheckDropRoleActor(v.View)
		var d authorization.RoleDescriptor
		if err == nil {
			d, err = role.DropRoleTarget(v.View, name)
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.acquireRolePrincipal(d.Ref, lock.AccessExclusive, false); err != nil {
			return err
		}
		if err := s.roleRetirementUnchanged(d); err != nil {
			return err
		}
		if err := s.acquireRolePrincipal(d.Ref, lock.AccessExclusive, true); err != nil {
			return err
		}
		if err := s.roleRetirementUnchanged(d); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) roleRetirementUnchanged(captured authorization.RoleDescriptor) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	if err := role.CheckDropRoleActor(v.View); err != nil {
		return err
	}
	if err := role.CheckDropRoleIdentity(v.View, captured.Ref); err != nil {
		return err
	}
	return checkRoleTupleUnchanged(v.Roles, captured)
}

func (s *StatementScope) acquireMembershipLocks(v AuthorizationView, statement *parser.RoleMembership, candidate *authorization.Root) error {
	for _, spec := range statement.Targets {
		ref, err := authorization.MembershipRole(v.Roles, v.State, spec)
		if err != nil {
			return err
		}
		if err := s.acquireRolePrincipal(ref, lock.ShareUpdateExclusive, false); err != nil {
			return err
		}
		if err := s.roleStillPresent(ref); err != nil {
			return err
		}
	}
	dependency := func(spec parser.RoleSpec) error {
		ref, err := authorization.MembershipRole(v.Roles, v.State, spec)
		if err != nil {
			return err
		}
		if err := s.acquireRolePrincipal(ref, lock.AccessShare, false); err != nil {
			return err
		}
		return s.roleStillPresent(ref)
	}
	for _, spec := range statement.Members {
		if err := dependency(spec); err != nil {
			return err
		}
	}
	if statement.Grantor != nil {
		if err := dependency(*statement.Grantor); err != nil {
			return err
		}
	}
	return s.acquireRoleDependencies(authorization.AddedMembershipReferences(&v.RoleRecord.Root, candidate))
}

func (s *StatementScope) acquireInitialMembershipLocks(base, candidate *authorization.Root, created authorization.PrincipalRef) error {
	for edge := range authorization.AddedMemberships(base, candidate) {
		if edge.Role != created {
			if err := s.acquireRolePrincipal(edge.Role, lock.ShareUpdateExclusive, false); err != nil {
				return err
			}
			if err := s.roleStillPresent(edge.Role); err != nil {
				return err
			}
		}
		for _, ref := range [...]authorization.PrincipalRef{edge.Role, edge.Member, edge.Grantor} {
			if ref == created {
				continue
			}
			if err := s.acquireRolePrincipal(ref, lock.AccessShare, false); err != nil {
				return err
			}
			if err := s.roleStillPresent(ref); err != nil {
				return err
			}
		}
	}
	return nil
}
