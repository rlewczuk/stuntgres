package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The ordinary default provider is metadata-only. Cook each physical default
// against its own column Usage, but publish the complete hierarchy only once.
// The caller holds Engine.mu; every return releases it.
func (e *Engine) alterInheritanceDefaultLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTableDefault, root *catalog.Relation) (Result, error) {
	targets, auth, err := scope.acquireInheritanceDefinitions(root, !raw.Only)
	if err != nil {
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, old := range targets {
		ref := scope.lockedRef
		ref.Address = old.Address()
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.Catalog)
		fresh, err := e.statementAuthorizationLocked(op, raw)
		if err == nil && current.Get(old.Address()) != old {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			err = fresh.Check(access.Requirement{Object: ref, OwnerOnly: true})
		}
		childSlot, exists := old.Index[raw.Column.Text]
		if err == nil && !exists {
			err = ids.ErrCatalogReplaced
		}
		var definition catalog.ColumnDefault
		if err == nil && raw.Action == parser.SetDefault {
			definition, err = e.cookAlterDefaultLocked(scope, op, auth, old, childSlot, raw.Expression)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		next, err := table.InheritanceDefaultCandidate(old, childSlot, definition)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		store := e.privateStorageLocked(scope.owner.Catalog, old.Address())
		if store == nil {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		length, generation, ends := len(store.Versions), store.Generation, store.Ends
		e.mu.Unlock()
		if err := stage.alterRelation(op.Context(), ref, next, store, store, length, generation, ends); err != nil {
			return Result{}, err
		}
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}
