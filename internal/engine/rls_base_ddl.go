package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Called by the existing ALTER executor with Engine.mu held. Flag-only lists
// stage only metadata: no storage clone, row scan, policy copy or definition OID
// reminting. The ordinary StatementScope owns transaction merge/publication.
func (e *Engine) stageRowSecurityFlagsLocked(scope *StatementScope, op *Operation, auth AuthorizationView, old, next *relationDescriptor) (Result, error) {
	scope.catalogAuthority = auth.catalogAuthorityLocked(scope.lockedRef, e.catalog.state.Get(old.address))
	if next == old {
		e.mu.Unlock()
		return Result{}, op.Context().Err()
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.mu.Unlock()
	if old.revision == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.revision = old.revision + 1
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	err := scope.validateLockedRelation(e)
	if err == nil {
		e.catalogChangeLocked(changes, old.address, next)
		scope.catalog = changes
	}
	e.mu.Unlock()
	return Result{}, err
}
