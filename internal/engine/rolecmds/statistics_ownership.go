package rolecmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// Capture under Mu, wait without Mu, then recheck live authority against the
// original generation/name/namespace and recipient. Never adopt a successor.
func executeStatisticsOwnership(e *core.Engine, scope *core.StatementScope, s *parser.AlterOwner, name string, identity bool, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	host := RoleHost{core.DDLHost{E: e, Op: scope.Op}}
	change, err := role.PlanStatisticsOwnership(host, auth, s, name, identity)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	address := change.Address
	if change.Object != nil {
		address = change.Object.Address()
	}
	original := auth.Catalog.Get(address)
	base := auth.Catalog
	namespace := base.Get(original.Namespace())
	target := change.Target
	e.Mu.Unlock()
	ref := e.Catalog.Domain.Ref(base.Epoch(), address)
	if err = scope.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
		return core.Result{}, err
	}
	// Statistics have no relation-ownership companions. Ordinary publication
	// admits their real definition dependencies and principal/namespace fences.
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(scope.Op)
	if err == nil && (auth.Catalog.Domain() != base.Domain() || auth.Catalog.Epoch() != base.Epoch() || auth.Catalog.NameAddress(catalog.DescriptorNameKey(original)) != address || auth.Catalog.Get(address) == nil || auth.Catalog.Get(address).Namespace() != original.Namespace() || namespace == nil || auth.Catalog.NameAddress(catalog.DescriptorNameKey(namespace)) != namespace.Address()) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		change, err = role.PlanStatisticsOwnership(host, auth, s, name, identity)
	}
	if err == nil && change.Target != target {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		current := change.Address
		if change.Object != nil {
			current = change.Object.Address()
		}
		if current != address {
			err = ids.ErrCatalogReplaced
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	if change.Object == nil {
		scope.RecordDDLCommand("ALTER STATISTICS", address)
		e.Mu.Unlock()
		scope.CompleteUtility()
		return core.Result{}, scope.Op.Context().Err()
	}
	stage := core.OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	stage.RecordDDLCommand("ALTER STATISTICS", address)
	return core.PublishOwnership(e, scope, stage)
}
