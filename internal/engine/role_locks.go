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
func (s *StatementScope) AcquireRoleResource(resource lock.Resource, mode lock.Mode) error {
	e := s.Engine()
	e.Mu.Lock()
	if resource.Domain() != &e.Cluster.RoleDomain || resource.Epoch != 1 || s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	err := e.AssignTransactionXIDLocked(s.TxRef)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.acquireTransactionResource(resource, mode); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() || s.Owner.Ref != s.TxRef {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

func (s *StatementScope) AcquireRolePrincipal(ref authorization.PrincipalRef, mode lock.Mode, tuple bool) error {
	e := s.Engine()
	if ref.ID() == 0 || ref.ID() > math.MaxUint32 || ref != e.RoleStore.Domain.Principal(ref.ID()) {
		return fmt.Errorf("stuntgres: foreign role lock principal")
	}
	resource := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: ids.OID(ref.ID())}), RoleTuple: tuple}
	return s.AcquireRoleResource(resource, mode)
}

func (s *StatementScope) RoleStillPresent(ref authorization.PrincipalRef) error {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	if _, ok := v.Roles.Find(ref); !ok {
		return SQLError("42704", fmt.Sprintf("role %d was concurrently dropped", ref.ID()))
	}
	return nil
}

func roleTupleUnchanged(s *StatementScope, captured authorization.RoleDescriptor) error {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	return checkRoleTupleUnchanged(v.Roles, captured)
}

func checkRoleTupleUnchanged(roles authorization.Read, captured authorization.RoleDescriptor) error {
	actual, exists := roles.Find(captured.Ref)
	if !exists {
		return SQLError("XX000", "tuple concurrently deleted")
	}
	if actual.AuthIDVersion != captured.AuthIDVersion {
		return SQLError("XX000", "tuple concurrently updated")
	}
	return nil
}

func acquireRoleAttributeLocks(s *StatementScope, stmt parser.Statement, target authorization.RoleDescriptor) error {
	if create, ok := stmt.(*parser.CreateRole); ok {
		e := s.Engine()
		resource := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.RoleClassID, Name: create.Name.Text}}
		if err := s.AcquireRoleResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.Mu.Lock()
		v, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		_, exists := v.Roles.Lookup(create.Name.Text)
		e.Mu.Unlock()
		if exists {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_authid_rolname_index"`, Detail: "Key (rolname)=(" + create.Name.Text + ") already exists.", Schema: "pg_catalog", Table: "pg_authid", Constraint: "pg_authid_rolname_index"}
		}
		return nil
	}
	if err := s.AcquireRolePrincipal(target.Ref, lock.AccessExclusive, true); err != nil {
		return err
	}
	return roleTupleUnchanged(s, target)
}

func acquireRoleRetirementLocks(s *StatementScope, statement *parser.DropRole) error {
	if s.Owner.State != session.TxActive && s.Owner.RetainsTransaction() {
		return nil
	}
	for _, name := range statement.Names {
		e := s.Engine()
		e.Mu.Lock()
		v, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		err = role.CheckDropRoleActor(v.View)
		var d authorization.RoleDescriptor
		if err == nil {
			d, err = role.DropRoleTarget(v.View, name)
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.AcquireRolePrincipal(d.Ref, lock.AccessExclusive, false); err != nil {
			return err
		}
		if err := roleRetirementUnchanged(s, d); err != nil {
			return err
		}
		if err := s.AcquireRolePrincipal(d.Ref, lock.AccessExclusive, true); err != nil {
			return err
		}
		if err := roleRetirementUnchanged(s, d); err != nil {
			return err
		}
	}
	return nil
}

func roleRetirementUnchanged(s *StatementScope, captured authorization.RoleDescriptor) error {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v, err := e.AuthorizationLocked(s.Op)
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

func acquireMembershipLocks(s *StatementScope, v AuthorizationView, statement *parser.RoleMembership, candidate *authorization.Root) error {
	for _, spec := range statement.Targets {
		ref, err := authorization.MembershipRole(v.Roles, v.State, spec)
		if err != nil {
			return err
		}
		if err := s.AcquireRolePrincipal(ref, lock.ShareUpdateExclusive, false); err != nil {
			return err
		}
		if err := s.RoleStillPresent(ref); err != nil {
			return err
		}
	}
	dependency := func(spec parser.RoleSpec) error {
		ref, err := authorization.MembershipRole(v.Roles, v.State, spec)
		if err != nil {
			return err
		}
		if err := s.AcquireRolePrincipal(ref, lock.AccessShare, false); err != nil {
			return err
		}
		return s.RoleStillPresent(ref)
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
	return s.AcquireRoleDependencies(authorization.AddedMembershipReferences(&v.RoleRecord.Root, candidate))
}

func acquireInitialMembershipLocks(s *StatementScope, base, candidate *authorization.Root, created authorization.PrincipalRef) error {
	for edge := range authorization.AddedMemberships(base, candidate) {
		if edge.Role != created {
			if err := s.AcquireRolePrincipal(edge.Role, lock.ShareUpdateExclusive, false); err != nil {
				return err
			}
			if err := s.RoleStillPresent(edge.Role); err != nil {
				return err
			}
		}
		for _, ref := range [...]authorization.PrincipalRef{edge.Role, edge.Member, edge.Grantor} {
			if ref == created {
				continue
			}
			if err := s.AcquireRolePrincipal(ref, lock.AccessShare, false); err != nil {
				return err
			}
			if err := s.RoleStillPresent(ref); err != nil {
				return err
			}
		}
	}
	return nil
}
