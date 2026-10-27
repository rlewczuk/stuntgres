package core

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

// AcquireRoleSetting holds principal retirement protection before the exact
// cluster scope writer. Both claims belong to the existing transaction ticket.
// Call unlocked; after return redecide target and parameter authority under Mu.
func (s *StatementScope) AcquireRoleSetting(key access.RoleSettingKey) error {
	if key.Role != (authorization.PrincipalRef{}) {
		if err := s.AcquireRolePrincipal(key.Role, lock.AccessShare, false); err != nil {
			return err
		}
		if err := s.RoleStillPresent(key.Role); err != nil {
			return err
		}
	}
	if key.Role.ID() > math.MaxUint32 {
		return fmt.Errorf("stuntgres: invalid role settings scope")
	}
	e := s.Engine()
	resource := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, key.Database), Kind: lock.RoleSettingResource, High: uint32(key.Role.ID())}
	if err := s.AcquireRoleResource(resource, lock.AccessExclusive); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.refreshOperationRolesLocked(s.Op)
}

// FenceRoleSettingDatabaseLocked retains the selected owner-dependent database
// observation in the same journal as the settings mutation. Caller holds Mu.
func (e *Engine) FenceRoleSettingDatabaseLocked(op *session.Operation, address ids.ObjectAddress, authority catalog.Descriptor) error {
	current := OperationCatalog(op)
	next := current.Clone()
	capture := session.JournalCapture{Journal: next, Current: e.PrivateCatalogLocked(current).State(), Committed: e.Catalog.State}
	if err := capture.ObserveDatabase(address, authority, e.Catalog.State.Get(address)); err != nil {
		return err
	}
	if scope := ActiveScopeOfLease(op.Lease); scope != nil {
		scope.Catalog = next
	} else {
		installOperationCatalog(op, next)
	}
	return nil
}
