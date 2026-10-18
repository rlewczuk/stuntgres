package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// The ordinary default provider is metadata-only. Cook each physical default
// against its own column Usage, but publish the complete hierarchy only once.
// The caller holds Engine.mu; every return releases it.
func (e *Engine) alterInheritanceDefaultLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableDefault, root *relationDescriptor) (Result, error) {
	targets, auth, err := scope.acquireInheritanceDefinitions(root, !raw.Only)
	if err != nil {
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, old := range targets {
		ref := scope.lockedRef
		ref.Address = old.address
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.catalog)
		fresh, err := e.statementAuthorizationLocked(op, raw)
		if err == nil && current.Get(old.address) != old {
			err = ErrCatalogReplaced
		}
		if err == nil {
			err = fresh.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		}
		childSlot, exists := old.index[raw.Column.Text]
		if err == nil && !exists {
			err = ErrCatalogReplaced
		}
		var definition columnDefault
		if err == nil && raw.Action == parser.SetDefault {
			definition, err = e.cookAlterDefaultLocked(scope, op, auth, old, childSlot, raw.Expression)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if old.revision == ids.DefinitionRevision(^uint64(0)) {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next := *old
		next.revision++
		next.defaults = maps.Clone(old.defaults)
		attribute := old.attributes[childSlot]
		if definition.expression == nil {
			delete(next.defaults, attribute)
		} else {
			if next.defaults == nil {
				next.defaults = make(map[int32]columnDefault)
			}
			next.defaults[attribute] = definition
		}
		if next.inheritance != nil {
			next.inheritance = cloneInheritance(old.inheritance)
			provenance := next.inheritance.columns[attribute]
			provenance.explicitDefault = true
			next.inheritance.columns[attribute] = provenance
		}
		store := e.privateStorageLocked(scope.owner.catalog, old.address)
		if store == nil {
			e.mu.Unlock()
			return Result{}, ErrCatalogReplaced
		}
		length, generation, ends := len(store.versions), store.generation, store.ends
		e.mu.Unlock()
		if err := stage.alterRelation(op.Context(), ref, &next, store, store, length, generation, ends); err != nil {
			return Result{}, err
		}
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}
