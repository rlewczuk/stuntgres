package engine

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func holdsColumnDropGrant(scope *StatementScope, ref ids.ObjectRef) bool {
	if scope.LockTicket == nil || scope.LockTicket.Ref() != scope.TxRef {
		return false
	}
	return scope.Engine().Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive)
}

// prepareColumnDrop records the actual ordinary provider effects without owning
// a catalog stage, visibility view, expression evaluation or physical rewrite.
func prepareColumnDrop(scope *StatementScope, auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction) (*table.DropPreparation, error) {
	return prepareColumnDropAfter(scope, auth, old, ref, action, nil)
}

// prior is the immediately preceding successful DROP preparation. Its ordered
// real effects remain visible to dependency discovery, not just its root shape.
func prepareColumnDropAfter(scope *StatementScope, auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction, prior *table.DropPreparation) (*table.DropPreparation, error) {
	provider, err := prepareDropDefinitionProvider(scope, auth, old, ref, prior)
	if err != nil {
		return nil, err
	}
	next, err := table.OrdinaryColumnDrop(provider, old, ref, action)
	if err != nil {
		return nil, err
	}
	return finishDropDefinition(scope, provider, next)
}

func prepareDropDefinitionProvider(scope *StatementScope, auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, prior *table.DropPreparation) (*DropProvider, error) {
	if err := scope.Active(); err != nil {
		return nil, err
	}
	e := scope.Engine()
	if err := scope.Op.Context().Err(); err != nil {
		return nil, err
	}
	if scope.Finished || auth.Owner != &e.Catalog.Owner || auth.Session != scope.Op.Session || auth.Catalog.Owner() != &e.Catalog.Owner || ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || ref.Epoch != scope.TxRef.Epoch() || old == nil || old.Address() != ref.Address || ref.Address.SubID != 0 || !holdsColumnDropGrant(scope, ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, present := auth.Catalog.Relation(ref.Address); !present {
		return nil, ids.ErrCatalogReplaced
	}
	if prior != nil && (prior.Statement != any(scope) || prior.TxRef != scope.TxRef || prior.Ref != ref || prior.Next() == nil) {
		return nil, ids.ErrCatalogReplaced
	}
	e.Mu.Lock()
	path, err := ReferencePath(auth)
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	prepared := &table.DropPreparation{Old: old, Ref: ref, Prior: prior, Statement: scope, TxRef: scope.TxRef, Base: auth.Catalog, Discovery: auth.Catalog, Input: dropDiscoveryInput(auth.Definition)}
	provider := &DropProvider{Base: auth.Catalog, Owner: e, SearchPath: path, Prepared: prepared, Ctx: scope.Op.Context(), Scope: scope}
	provider.Observe(ref.Address)
	return provider, nil
}

func finishDropDefinition(scope *StatementScope, provider *DropProvider, next *catalog.Relation) (*table.DropPreparation, error) {
	prepared := provider.Prepared
	prepared.Finish(next)
	e := provider.Owner
	e.Mu.Lock()
	err := validateColumnDropObservationsLocked(scope, prepared, e.PrivateCatalogLocked(scope.Owner.Catalog))
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// validateColumnDropObservationsLocked never treats an additive catalog member
// as replacement. Only captured provider identities and the operation are fenced.
func validateColumnDropObservationsLocked(scope *StatementScope, prepared *table.DropPreparation, catalog catalog.Read) error {
	if prepared == nil || scope.Finished {
		return ids.ErrCatalogReplaced
	}
	return prepared.ValidateObservations(scope, scope.TxRef, catalog)
}

// consumePreparedDrop applies the exact immutable provider effects to the sole
// command stage. Neither dependency analysis nor metadata cooking runs again.
func consumePreparedDrop(scope *StatementScope, stage *CatalogStage, prepared *table.DropPreparation, old *catalog.Relation, ref ids.ObjectRef, changes *session.Journal) (*catalog.Relation, error) {
	if err := scope.Active(); err != nil {
		return nil, err
	}
	if err := scope.Op.Context().Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.Old != old || prepared.Ref != ref || prepared.Next() == nil || stage == nil || stage.Owner != scope.Engine() || stage.Lifecycle != CatalogOpen || stage.Invalid != nil || !holdsColumnDropGrant(scope, ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, consumed := scopeDropsOrZero(scope).ConsumedColumns[prepared]; consumed {
		return nil, ids.ErrCatalogReplaced
	}
	if prepared.Prior != nil {
		if _, consumed := scopeDropsOrZero(scope).ConsumedColumns[prepared.Prior]; !consumed {
			return nil, ids.ErrCatalogReplaced
		}
	}
	for _, grant := range prepared.Grants {
		if !holdsColumnDropGrant(scope, grant) {
			return nil, ids.ErrCatalogReplaced
		}
	}
	e := stage.Owner
	e.Mu.Lock()
	err := validateColumnDropObservationsLocked(scope, prepared, stage.Base)
	if err == nil {
		err = validateColumnDropObservationsLocked(scope, prepared, e.PrivateCatalogLocked(scope.Owner.Catalog))
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	if scopeDropsOrZero(scope).ConsumedColumns == nil {
		scopeDrops(scope).ConsumedColumns = make(map[*table.DropPreparation]struct{})
	}
	scopeDrops(scope).ConsumedColumns[prepared] = struct{}{}
	provider := StageDropProvider(scope.Op.Context(), stage, scope, changes, nil)
	for _, effect := range prepared.Effects() {
		if err := scope.Op.Context().Err(); err != nil {
			return nil, err
		}
		switch {
		case effect.Descriptor != nil:
			if err := stage.ReplaceDescriptor(effect.Descriptor, false, false); err != nil {
				return nil, err
			}
		case effect.Remove.Address != (ids.ObjectAddress{}):
			e.Mu.Lock()
			err = provider.RecordRemoval(effect.Remove, false)
			e.Mu.Unlock()
			if err != nil {
				return nil, err
			}
		case effect.Before != nil:
			if err := projectPreparedColumn(provider, effect.Before, effect.After); err != nil {
				return nil, err
			}
		case effect.Notice != "":
			stage.DropCascadeNames = append(stage.DropCascadeNames, effect.Notice)
		case effect.Missing != nil:
			scope.Op.EmitNotice(session.LevelNotice, *effect.Missing)
		case effect.RowType != (ids.ObjectAddress{}):
			stage.DomainTargets = append(stage.DomainTargets, effect.RowType)
		case len(effect.IdentityRoots) != 0:
			state := effect.IdentityState
			if state == nil {
				state = prepared.Base.State()
			}
			stage.SequenceState().ObserveIdentityDependencies(session.DependencyObservation{state, effect.IdentityRoots})
		case effect.Layout != nil:
			provider.RecordIndexLayout(effect.Index, *effect.Layout)
		}
	}
	return prepared.Next(), nil
}

func projectPreparedColumn(p *DropProvider, old, next *catalog.Relation) error {
	if old.RelationKind == catalog.CompositeRelation || old.RelationKind == catalog.ForeignRelation {
		return p.Stage.ReplaceDescriptor(next, false, false)
	}
	if p.Changes != nil {
		return stagePreparedColumnShape(p.Scope, p.Stage, p.Changes, old, next)
	}
	// Non-SQL physical retirement already separates metadata from the storage
	// projector. This path retains its actual storage/lifetime checks.
	return p.ProjectColumn(old, next)
}

// DropScope is the statement's DROP slot: prepared column drops already
// consumed by this statement. StatementScope.DropsSlot holds it as an opaque
// StatementSlot.
type DropScope struct {
	ConsumedColumns map[*table.DropPreparation]struct{}
}

// CloneSlot copies the slot for a PL/pgSQL exception checkpoint.
func (d *DropScope) CloneSlot() StatementSlot {
	owned := *d
	owned.ConsumedColumns = maps.Clone(owned.ConsumedColumns)
	return &owned
}

// dropScopeOf recovers the drops slot. Only DropScope is ever stored there;
// meeting another type is a bug.
func dropScopeOf(slot StatementSlot) *DropScope {
	d, ok := slot.(*DropScope)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected drops slot %T", slot))
	}
	return d
}

// drops returns the statement's drops slot, allocating it on first use.
func scopeDrops(s *StatementScope) *DropScope {
	if s.DropsSlot == nil {
		s.DropsSlot = new(DropScope)
	}
	return dropScopeOf(s.DropsSlot)
}

// dropsOrZero reads the drops slot; a nil slot reads as zero.
func scopeDropsOrZero(s *StatementScope) DropScope {
	if s.DropsSlot == nil {
		return DropScope{}
	}
	return *dropScopeOf(s.DropsSlot)
}

// dropDiscoveryInput is the definition a prepared drop discovers dependents
// through. An inheritance node reader's observations fence only the node's
// own cooking, so the drop reads through a copy that records nothing.
func dropDiscoveryInput(input catalog.DefinitionReader) catalog.DefinitionReader {
	if reader, present := input.(table.InheritanceDefinitionReader); present {
		return reader.DiscoveryReader()
	}
	return input
}
