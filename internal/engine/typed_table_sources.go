package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

func acquireTypedSources(scope *core.StatementScope, stage *core.CatalogStage, drop bool) error {
	if stage == nil || stage.Owner != scope.Engine() || stage.Base.Domain() != scope.Engine().Catalog.Domain || stage.Base.Epoch() != scope.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	addresses := table.TypedSources(drop, stage.DomainTargets, stage.Descriptors())
	if len(addresses) == 0 {
		return nil
	}
	mode := lock.AccessShare
	if drop {
		mode = lock.AccessExclusive
	}
	for _, source := range addresses {
		typ, ok := stage.Base.Type(source)
		if !ok {
			continue
		}
		if drop {
			// General DROP roots include scalar/domain types, whose actual
			// resource class is TypeExclusive, not a zero/fabricated relation.
			if err := scope.AcquireSequence(stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), source), lock.TypeExclusive, true); err != nil {
				return err
			}
			e := stage.Owner
			e.Mu.Lock()
			valid := e.PrivateCatalogLocked(scope.Owner.Catalog).Get(source) == typ
			e.Mu.Unlock()
			if !valid {
				return catalog.Conflict{}
			}
		}
		// Only a real composite source has a backing relation to lock.
		if !typ.SQLType.IsComposite() || typ.OwningRelation == (ids.ObjectAddress{}) {
			continue
		}
		resource := typ.OwningRelation
		if err := scope.AcquireSequence(stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), resource), mode, true); err != nil {
			return err
		}
		// A wait may admit a different definition at the same address. Keep
		// the copied shape tied to the exact captured reciprocal providers;
		// neither a new generation nor a same-name source can be adopted.
		e := stage.Owner
		e.Mu.Lock()
		current := e.PrivateCatalogLocked(scope.Owner.Catalog)
		valid := current.Get(source) == typ && current.Get(resource) == stage.Base.Get(resource)
		e.Mu.Unlock()
		if !valid {
			return catalog.Conflict{}
		}
	}
	return nil
}
