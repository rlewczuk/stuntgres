package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The ordinary default provider is metadata-only. Cook each physical default
// against its own column Usage, but publish the complete hierarchy only once.
// The caller holds Engine.mu; every return releases it.
func alterInheritanceDefaultLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTableDefault, root *catalog.Relation) (core.Result, error) {
	targets, auth, err := acquireInheritanceDefinitions(scope, root, !raw.Only)
	if err != nil {
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, old := range targets {
		ref := scope.LockedRef
		ref.Address = old.Address()
		e.Mu.Lock()
		current := e.PrivateCatalogLocked(scope.Owner.Catalog)
		fresh, err := e.StatementAuthorizationLocked(op, raw)
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
			definition, err = cookAlterDefaultLocked(e, scope, op, auth, old, childSlot, raw.Expression)
		}
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		next, err := table.InheritanceDefaultCandidate(old, childSlot, definition)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		store := e.PrivateStorageLocked(scope.Owner.Catalog, old.Address())
		if store == nil {
			e.Mu.Unlock()
			return core.Result{}, ids.ErrCatalogReplaced
		}
		length, generation, ends := len(store.Versions), store.Generation, store.Ends
		e.Mu.Unlock()
		if err := stage.AlterRelation(op.Context(), ref, next, store, store, length, generation, ends); err != nil {
			return core.Result{}, err
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
	}
	if err == nil {
		scope.Completed = true
	}
	return core.Result{}, err
}
