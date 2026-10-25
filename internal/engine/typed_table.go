package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// OF names are catalog object names, not SQL declaration aliases.
func (e *Engine) namedType(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, error) {
	path, err := e.lookupNamespaces(auth, name, analyze.TypeLookup)
	if err != nil {
		return nil, err
	}
	for i := range path.Count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 1 {
			return auth.Catalog.MustType(found[0]), nil
		}
	}
	return nil, expr.ErrorAt("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)), name.Parts[0].Pos)
}

func (e *Engine) alterTableOfLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTableOf, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	old, ok := scope.LockedDescriptor.(*catalog.Relation)
	if !ok {
		return fail(ids.ErrCatalogReplaced)
	}
	next, err := table.AlterTableOf(DDLHost{E: e, Op: op}, auth, old, s)
	if err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if next.OfType != (ids.ObjectAddress{}) {
		stage.DomainBases = append(stage.DomainBases, auth.Catalog.Get(next.OfType))
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if s.Type != nil {
		// Execution can select a source created while target admission waited.
		// Grant the actual staged attachment, not just the pre-wait source.
		replacementRead := scope.ReplacementRead
		if err := acquireTypedSources(scope, stage, false); err != nil {
			return Result{}, err
		}
		// Release only this helper's incidental reservation before provider
		// callbacks; preserve any replacement admission held on entry.
		if !replacementRead && scope.ReplacementRead {
			e.ReplacementMu.RUnlock()
			scope.ReplacementRead = false
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.Completed = true
	}
	return Result{}, err
}

func acquireTypedSources(scope *StatementScope, stage *CatalogStage, drop bool) error {
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
