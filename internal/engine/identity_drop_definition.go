package engine

import (
	"fmt"
	"iter"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// An identity DROP owns its real grant-time sequence capture separately from
// the table's phase input and the committed publication predecessor.
type preparedIdentityDrop struct {
	old      *catalog.Relation
	ref      ids.ObjectRef
	next     *catalog.Relation
	drop     *table.DropPreparation
	sequence ids.ObjectRef
	original catalog.Descriptor
}

func prepareIdentityDropDefinition(e *Engine, scope *StatementScope, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*preparedIdentityDrop, error) {
	if action.Action != parser.DropIdentity {
		return nil, fmt.Errorf("stuntgres: invalid identity drop")
	}
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{})
	if old != nil {
		ref.Address = old.Address()
	}
	provider, err := prepareDropDefinitionProvider(scope, auth, old, ref, nil)
	if err != nil {
		return nil, err
	}
	if provider.Owner != e {
		return nil, ids.ErrCatalogReplaced
	}
	next, err := table.DropIdentity(provider, old, action)
	if err != nil {
		return nil, err
	}
	capture := provider.IdentityDrop
	drop, err := finishDropDefinition(scope, provider, next)
	if err != nil {
		return nil, err
	}
	return &preparedIdentityDrop{old: old, ref: ref, next: next, drop: drop, sequence: capture.Sequence, original: capture.Original}, nil
}

func consumeIdentityDropDefinition(scope *StatementScope, stage *CatalogStage, prepared *preparedIdentityDrop, old *catalog.Relation, ref ids.ObjectRef) (*catalog.Relation, error) {
	if err := scope.Active(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.old != old || prepared.ref != ref || prepared.drop == nil {
		return nil, ids.ErrCatalogReplaced
	}
	e := scope.Engine()
	if prepared.sequence.Address != (ids.ObjectAddress{}) {
		e.Mu.Lock()
		unchanged := e.Catalog.State.Get(prepared.sequence.Address) == prepared.original
		e.Mu.Unlock()
		if !unchanged {
			return nil, ids.ErrCatalogReplaced
		}
	}
	next, err := consumePreparedDrop(scope, stage, prepared.drop, old, ref, nil)
	if err != nil {
		return nil, err
	}
	if prepared.sequence.Address != (ids.ObjectAddress{}) {
		stage.SequenceState().CaptureIdentityOriginal(prepared.sequence.Address, prepared.original)
		ApplyIdentityDropTombstone(stage, prepared.sequence.Address)
	}
	return next, nil
}

// Next is the relation the prepared identity drop leaves.
func (prepared *preparedIdentityDrop) Next() *catalog.Relation { return prepared.next }

// DefinitionDelta reports only real effects. The caller's linked reader owns
// authentic fallback and resolution observations, never this preparation's
// input reader.
func (prepared *preparedIdentityDrop) DefinitionDelta(address ids.ObjectAddress) (catalog.Descriptor, bool, bool) {
	if address == prepared.ref.Address {
		return prepared.next, true, true
	}
	return prepared.drop.DefinitionDelta(address)
}

// LayoutDelta is the identity drop's index layout, as IndexLayout reads it.
func (prepared *preparedIdentityDrop) LayoutDelta(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	return prepared.IndexLayout(address)
}

func (prepared *preparedIdentityDrop) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if descriptor, present, handled := prepared.DefinitionDelta(address); handled {
		return descriptor, present
	}
	return prepared.drop.Descriptor(address)
}
func (prepared *preparedIdentityDrop) Lookup(key ids.NameKey) ids.ObjectAddress {
	return prepared.drop.Lookup(key)
}
func (prepared *preparedIdentityDrop) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range prepared.drop.Descriptors() {
			if descriptor, present := prepared.Descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (prepared *preparedIdentityDrop) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return func(yield func(*catalog.Procedure) bool) {
		for candidate := range prepared.drop.ProceduresNamed(namespace, name) {
			descriptor, present := prepared.Descriptor(candidate.Address())
			if d, ok := descriptor.(*catalog.Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}
func (prepared *preparedIdentityDrop) HasDynamicProcedureName(name string) bool {
	return prepared.drop.HasDynamicProcedureName(name)
}

func (prepared *preparedIdentityDrop) HasProviderProcedureOverride(name string) bool {
	return prepared.drop.HasProviderProcedureOverride(name)
}
func (prepared *preparedIdentityDrop) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	return prepared.drop.IndexLayout(address)
}
