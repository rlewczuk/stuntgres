package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Called by the existing ALTER executor with Engine.mu held. Flag-only and
// relation-option lists stage only metadata: no storage clone, row scan, policy
// copy or definition OID reminting. StatementScope owns merge/publication.
func (e *Engine) stageRowSecurityFlagsLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, old, next *catalog.Relation) (Result, error) {
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(scope.LockedRef, e.Catalog.State.Get(old.Address()))
	if next == old {
		e.Mu.Unlock()
		return Result{}, op.Context().Err()
	}
	changes := scope.Owner.Catalog.Clone()
	e.Mu.Unlock()
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision = old.Revision() + 1
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	err := scope.ValidateLockedRelation(e)
	if err == nil {
		e.CatalogChangeLocked(changes, old.Address(), next)
		scope.Catalog = changes
	}
	e.Mu.Unlock()
	return Result{}, err
}
