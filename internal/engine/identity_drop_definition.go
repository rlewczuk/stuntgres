package engine

import (
	"fmt"
	"iter"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
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

// CaptureIdentitySequence waits for the exact sequence's AX grant, then in one
// Engine.mu window captures its definition: refreshed into the stage, or as
// the prepared drop's immutable observation.
func (p *dropProvider) CaptureIdentitySequence(relation *catalog.Relation, attribute int32, ref ids.ObjectRef) error {
	// Do not capture the sequence definition before its actual operation grant:
	// the incumbent refresh selects this provider at grant time, not by name.
	if err := p.scope.acquireSequence(ref, lock.AccessExclusive, false); err != nil {
		return err
	}
	e := p.owner
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.stage == nil {
		capture, err := e.captureIdentityDropDefinitionLocked(p, relation, attribute, ref)
		p.identityDrop = capture
		return err
	}
	if err := e.refreshIdentitySequenceLocked(p.scope, p.stage, relation, attribute, ref.Address); err != nil {
		return err
	}
	p.base = p.stage.Base
	p.identityDrop = identityDropCapture{sequence: ref, original: e.catalog.state.Get(ref.Address)}
	p.stage.sequenceState().ObserveIdentityDependencies(session.DependencyObservation{e.catalog.state, []ids.ObjectAddress{ref.Address}})
	return nil
}

func (p *dropProvider) TombstoneIdentity(address ids.ObjectAddress) {
	if p.stage != nil {
		applyIdentityDropTombstone(p.stage, address)
	}
}

// The caller holds Engine.mu after the exact sequence's AX grant. This captures
// existing immutable definitions only: no generator, scalar or stage is touched.
func (e *Engine) captureIdentityDropDefinitionLocked(provider *dropProvider, table *catalog.Relation, attribute int32, ref ids.ObjectRef) (identityDropCapture, error) {
	var capture identityDropCapture
	fresh, err := e.authorizationLocked(provider.scope.op)
	if err != nil {
		return capture, err
	}
	if fresh.Catalog.Epoch() != provider.base.Epoch() || provider.Removed(ref.Address) {
		return capture, ids.ErrCatalogReplaced
	}
	descriptor := fresh.Catalog.Get(ref.Address)
	if staged := provider.Replacement(ref.Address); staged != nil {
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
	stage.sequence.DeleteGenerator(address)
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
	next, err := table.DropIdentity(provider, old, action)
	if err != nil {
		return nil, err
	}
	capture := provider.identityDrop
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
		stage.sequenceState().CaptureIdentityOriginal(prepared.sequence.Address, prepared.original)
		applyIdentityDropTombstone(stage, prepared.sequence.Address)
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
