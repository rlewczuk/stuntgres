package engine

import (
	"fmt"
	"iter"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// An identity DROP owns its real grant-time sequence capture separately from
// the table's phase input and the committed publication predecessor.
type preparedIdentityDrop struct {
	old      *catalog.Relation
	ref      ids.ObjectRef
	next     *catalog.Relation
	drop     *preparedDrop
	sequence ids.ObjectRef
	original catalog.Descriptor
}

type identityDropCapture struct {
	sequence ids.ObjectRef
	original catalog.Descriptor
}

// dropIdentityDefinition is the incumbent provider algorithm shared by ordinary
// execution and immutable preparation. Only the execution sink owns a stage.
func (e *Engine) dropIdentityDefinition(provider *dropProvider, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, identityDropCapture, error) {
	var capture identityDropCapture
	if catalog.IsSystemColumn(action.Column.Text) {
		return old, capture, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	slot, present := old.Index[action.Column.Text]
	if !present {
		return old, capture, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.Name()))
	}
	attribute := old.Attributes[slot]
	identity, exists := old.ColumnMeta.Identities[attribute]
	if !exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" is not an identity column`, action.Column.Text, old.Name())
		if !action.IfExists {
			return old, capture, sqlError("55000", message)
		}
		notice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message + ", skipping"}
		if provider.prepared == nil {
			provider.scope.op.emitNotice(levelNotice, notice)
		} else {
			provider.prepared.recordEffect(dropEffect{missing: &notice})
		}
		return old, capture, nil
	}
	ref := e.catalog.Domain.Ref(provider.base.Epoch(), identity.Sequence)
	// Do not capture the sequence definition before its actual operation grant:
	// the incumbent refresh selects this provider at grant time, not by name.
	if err := provider.scope.acquireSequence(ref, lock.AccessExclusive, false); err != nil {
		return old, capture, err
	}
	e.mu.Lock()
	var err error
	if provider.stage != nil {
		err = e.refreshIdentitySequenceLocked(provider.scope, provider.stage, old, attribute, identity.Sequence)
		if err == nil {
			provider.base = provider.stage.Base
			capture = identityDropCapture{sequence: ref, original: e.catalog.state.Get(identity.Sequence)}
			provider.stage.sequenceState().observeIdentityDependencies(catalogDependencyObservation{e.catalog.state, []ids.ObjectAddress{identity.Sequence}})
		}
	} else {
		capture, err = e.captureIdentityDropDefinitionLocked(provider, old, attribute, ref)
	}
	e.mu.Unlock()
	if err != nil {
		return old, capture, err
	}
	if err := provider.applyViewDependents([]ids.ObjectAddress{identity.Sequence}, parser.DropRestrict); err != nil {
		return old, capture, err
	}
	if err := provider.remove(ref); err != nil {
		return old, capture, err
	}
	if provider.stage != nil {
		applyIdentityDropTombstone(provider.stage, identity.Sequence)
	}
	next := *old
	next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
	delete(next.ColumnMeta.Identities, attribute)
	return &next, capture, nil
}

// The caller holds Engine.mu after the exact sequence's AX grant. This captures
// existing immutable definitions only: no generator, scalar or stage is touched.
func (e *Engine) captureIdentityDropDefinitionLocked(provider *dropProvider, table *catalog.Relation, attribute int32, ref ids.ObjectRef) (identityDropCapture, error) {
	var capture identityDropCapture
	fresh, err := e.authorizationLocked(provider.scope.op)
	if err != nil {
		return capture, err
	}
	if fresh.Catalog.Epoch() != provider.base.Epoch() || provider.removed(ref.Address) {
		return capture, ids.ErrCatalogReplaced
	}
	descriptor := fresh.Catalog.Get(ref.Address)
	if staged := provider.replacement(ref.Address); staged != nil {
		descriptor = staged
	}
	sequence, err := sequence.IdentityDefinition(descriptor, table, attribute)
	if err != nil {
		return capture, err
	}
	// An earlier genuine observation is never overwritten by the refresh.
	observed := fresh.Catalog.Get(ref.Address)
	if prior, captured := provider.prepared.observed[ref.Address]; captured && prior != observed {
		return capture, ids.ErrCatalogReplaced
	}
	provider.prepared.observed[ref.Address] = observed
	provider.prepared.grants = append(provider.prepared.grants, ref)
	provider.base = fresh.Catalog
	provider.prepared.discovery = fresh.Catalog
	provider.identitySource = sequence
	provider.prepared.recordEffect(dropEffect{identityRoots: []ids.ObjectAddress{ref.Address}, identityState: e.catalog.state})
	return identityDropCapture{sequence: ref, original: e.catalog.state.Get(ref.Address)}, nil
}

func applyIdentityDropTombstone(stage *CatalogStage, address ids.ObjectAddress) {
	// A TYPE before-command may have staged AS. The real tombstone wins over
	// that private descriptor/scalar without modifying any live generator.
	stage.ForgetDescriptor(address)
	stage.sequence.deleteGenerator(address)
}

func (e *Engine) prepareIdentityDropDefinition(scope *StatementScope, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*preparedIdentityDrop, error) {
	if action.Action != parser.DropIdentity {
		return nil, fmt.Errorf("stuntgres: invalid identity drop")
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{})
	if old != nil {
		ref.Address = old.Address()
	}
	provider, err := scope.prepareDropDefinitionProvider(auth, old, ref, nil)
	if err != nil {
		return nil, err
	}
	if provider.owner != e {
		return nil, ids.ErrCatalogReplaced
	}
	next, capture, err := e.dropIdentityDefinition(provider, old, action)
	if err != nil {
		return nil, err
	}
	drop, err := scope.finishDropDefinition(provider, next)
	if err != nil {
		return nil, err
	}
	return &preparedIdentityDrop{old: old, ref: ref, next: next, drop: drop, sequence: capture.sequence, original: capture.original}, nil
}

func (scope *StatementScope) consumeIdentityDropDefinition(stage *CatalogStage, prepared *preparedIdentityDrop, old *catalog.Relation, ref ids.ObjectRef) (*catalog.Relation, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.old != old || prepared.ref != ref || prepared.drop == nil {
		return nil, ids.ErrCatalogReplaced
	}
	e := scope.engine()
	if prepared.sequence.Address != (ids.ObjectAddress{}) {
		e.mu.Lock()
		unchanged := e.catalog.state.Get(prepared.sequence.Address) == prepared.original
		e.mu.Unlock()
		if !unchanged {
			return nil, ids.ErrCatalogReplaced
		}
	}
	next, err := scope.consumePreparedDrop(stage, prepared.drop, old, ref, nil)
	if err != nil {
		return nil, err
	}
	if prepared.sequence.Address != (ids.ObjectAddress{}) {
		stage.sequenceState().captureIdentityOriginal(prepared.sequence.Address, prepared.original)
		applyIdentityDropTombstone(stage, prepared.sequence.Address)
	}
	return next, nil
}

// delta reports only real effects. The caller's linked reader owns authentic
// fallback and resolution observations, never this preparation's input reader.
func (prepared *preparedIdentityDrop) delta(address ids.ObjectAddress) (catalog.Descriptor, bool, bool) {
	if address == prepared.ref.Address {
		return prepared.next, true, true
	}
	if slot := prepared.drop.slots[address]; slot.descriptor != 0 {
		if slot.removed {
			return nil, false, true
		}
		effect := prepared.drop.effects[slot.descriptor-1]
		if effect.after != nil {
			return effect.after, true, true
		}
		return effect.descriptor, effect.descriptor != nil, true
	}
	return nil, false, false
}

func (prepared *preparedIdentityDrop) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if descriptor, present, handled := prepared.delta(address); handled {
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
func (prepared *preparedIdentityDrop) HasSQLProcedureName(name string) bool {
	return prepared.drop.HasSQLProcedureName(name)
}
func (prepared *preparedIdentityDrop) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	return prepared.drop.indexLayout(address)
}
