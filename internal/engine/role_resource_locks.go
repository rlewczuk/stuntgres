package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
