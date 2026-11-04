package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Called by the existing ALTER executor with Engine.mu held. Flag-only and
// relation-option lists stage only metadata: no storage clone, row scan, policy
// copy or definition OID reminting. StatementScope owns merge/publication.
func stageRowSecurityFlagsLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old, next *catalog.Relation, actions []parser.AlterTableAction) (core.Result, error) {
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(scope.LockedRef, e.Catalog.State.Get(old.Address()))
	for _, action := range actions {
		if table.RelationOptionsAction(action.Action) && len(action.Options) == 0 {
			continue
		}
		if err := auth.View.SepgsqlRelationPostAlter(next); err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
	}
	if next == old {
		e.Mu.Unlock()
		return core.Result{}, op.Context().Err()
	}
	changes := scope.PendingCatalog().Clone()
	e.Mu.Unlock()
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return core.Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision = old.Revision() + 1
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return core.Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	err := scope.ValidateLockedRelation(e)
	if err == nil {
		e.CatalogChangeLocked(changes, old.Address(), next)
		scope.Catalog = changes
	}
	e.Mu.Unlock()
	return core.Result{}, err
}
