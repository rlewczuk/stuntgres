package engine

import (
	"fmt"
	"iter"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// An identity DROP owns its real grant-time sequence capture separately from
// the table's phase input and the committed publication predecessor.
type preparedIdentityDrop struct {
	old      *relationDescriptor
	ref      ids.ObjectRef
	next     *relationDescriptor
	drop     *preparedDrop
	sequence ids.ObjectRef
	original Descriptor
}

type identityDropCapture struct {
	sequence ids.ObjectRef
	original Descriptor
}

func identitySequenceDefinition(descriptor Descriptor, table *relationDescriptor, attribute int32) (*relationDescriptor, error) {
	sequence, ok := descriptor.(*relationDescriptor)
	owned := table.address
	owned.SubID = attribute
	if !ok || sequence == nil || sequence.sequence == nil || !sequence.sequence.identity || sequence.sequence.owned != owned {
		return nil, ids.ErrCatalogReplaced
	}
	return sequence, nil
}

// dropIdentityDefinition is the incumbent provider algorithm shared by ordinary
// execution and immutable preparation. Only the execution sink owns a stage.
func (e *Engine) dropIdentityDefinition(provider *dropProvider, old *relationDescriptor, action parser.AlterTableAction) (*relationDescriptor, identityDropCapture, error) {
	var capture identityDropCapture
	if isSystemColumn(action.Column.Text) {
		return old, capture, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	slot, present := old.index[action.Column.Text]
	if !present {
		return old, capture, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.name))
	}
	attribute := old.attributes[slot]
	identity, exists := old.identities[attribute]
	if !exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" is not an identity column`, action.Column.Text, old.name)
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
	ref := e.catalog.domain.Ref(provider.base.Epoch(), identity.sequence)
	// Do not capture the sequence definition before its actual operation grant:
	// the incumbent refresh selects this provider at grant time, not by name.
	if err := provider.scope.acquireSequence(ref, lock.AccessExclusive, false); err != nil {
		return old, capture, err
	}
	e.mu.Lock()
	var err error
	if provider.stage != nil {
		err = e.refreshIdentitySequenceLocked(provider.scope, provider.stage, old, attribute, identity.sequence)
		if err == nil {
			provider.base = provider.stage.base
			capture = identityDropCapture{sequence: ref, original: e.catalog.state.Get(identity.sequence)}
			provider.stage.identityDependencies = append(provider.stage.identityDependencies, catalogDependencyObservation{e.catalog.state, []ids.ObjectAddress{identity.sequence}})
		}
	} else {
		capture, err = e.captureIdentityDropDefinitionLocked(provider, old, attribute, ref)
	}
	e.mu.Unlock()
	if err != nil {
		return old, capture, err
	}
	if err := provider.applyViewDependents([]ids.ObjectAddress{identity.sequence}, parser.DropRestrict); err != nil {
		return old, capture, err
	}
	if err := provider.remove(ref); err != nil {
		return old, capture, err
	}
	if provider.stage != nil {
		applyIdentityDropTombstone(provider.stage, identity.sequence)
	}
	next := *old
	next.identities = maps.Clone(old.identities)
	delete(next.identities, attribute)
	return &next, capture, nil
}

// The caller holds Engine.mu after the exact sequence's AX grant. This captures
// existing immutable definitions only: no generator, scalar or stage is touched.
func (e *Engine) captureIdentityDropDefinitionLocked(provider *dropProvider, table *relationDescriptor, attribute int32, ref ids.ObjectRef) (identityDropCapture, error) {
	var capture identityDropCapture
	fresh, err := e.authorizationLocked(provider.scope.op)
	if err != nil {
		return capture, err
	}
	if fresh.catalog.Epoch() != provider.base.Epoch() || provider.removed(ref.Address) {
		return capture, ids.ErrCatalogReplaced
	}
	descriptor := fresh.catalog.Get(ref.Address)
	if staged := provider.replacement(ref.Address); staged != nil {
		descriptor = staged
	}
	sequence, err := identitySequenceDefinition(descriptor, table, attribute)
	if err != nil {
		return capture, err
	}
	// An earlier genuine observation is never overwritten by the refresh.
	observed := fresh.catalog.Get(ref.Address)
	if prior, captured := provider.prepared.observed[ref.Address]; captured && prior != observed {
		return capture, ids.ErrCatalogReplaced
	}
	provider.prepared.observed[ref.Address] = observed
	provider.prepared.grants = append(provider.prepared.grants, ref)
	provider.base = fresh.catalog
	provider.prepared.discovery = fresh.catalog
	provider.identitySource = sequence
	provider.prepared.recordEffect(dropEffect{identityRoots: []ids.ObjectAddress{ref.Address}, identityState: e.catalog.state})
	return identityDropCapture{sequence: ref, original: e.catalog.state.Get(ref.Address)}, nil
}

func applyIdentityDropTombstone(stage *CatalogStage, address ids.ObjectAddress) {
	// A TYPE before-command may have staged AS. The real tombstone wins over
	// that private descriptor/scalar without modifying any live generator.
	delete(stage.descriptors, address)
	delete(stage.generators, address)
}

func (e *Engine) prepareIdentityDropDefinition(scope *StatementScope, auth AuthorizationView, old *relationDescriptor, action parser.AlterTableAction) (*preparedIdentityDrop, error) {
	if action.Action != parser.DropIdentity {
		return nil, fmt.Errorf("stuntgres: invalid identity drop")
	}
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{})
	if old != nil {
		ref.Address = old.address
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

func (scope *StatementScope) consumeIdentityDropDefinition(stage *CatalogStage, prepared *preparedIdentityDrop, old *relationDescriptor, ref ids.ObjectRef) (*relationDescriptor, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.old != old || prepared.ref != ref || prepared.drop == nil {
		return nil, ids.ErrCatalogReplaced
	}
	e := scope.op.session.Engine()
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
		if stage.identityOriginals == nil {
			stage.identityOriginals = make(map[ids.ObjectAddress]Descriptor)
		}
		if _, captured := stage.identityOriginals[prepared.sequence.Address]; !captured {
			stage.identityOriginals[prepared.sequence.Address] = prepared.original
		}
		applyIdentityDropTombstone(stage, prepared.sequence.Address)
	}
	return next, nil
}

// delta reports only real effects. The caller's linked reader owns authentic
// fallback and resolution observations, never this preparation's input reader.
func (prepared *preparedIdentityDrop) delta(address ids.ObjectAddress) (Descriptor, bool, bool) {
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

func (prepared *preparedIdentityDrop) descriptor(address ids.ObjectAddress) (Descriptor, bool) {
	if descriptor, present, handled := prepared.delta(address); handled {
		return descriptor, present
	}
	return prepared.drop.descriptor(address)
}
func (prepared *preparedIdentityDrop) lookup(key ids.NameKey) ids.ObjectAddress {
	return prepared.drop.lookup(key)
}
func (prepared *preparedIdentityDrop) descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address := range prepared.drop.descriptors() {
			if descriptor, present := prepared.descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}
func (prepared *preparedIdentityDrop) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	return prepared.drop.indexLayout(address)
}
