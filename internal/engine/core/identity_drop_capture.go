package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

type identityDropCapture struct {
	Sequence ids.ObjectRef
	Original catalog.Descriptor
}

// CaptureIdentitySequence waits for the exact sequence's AX grant, then in one
// Engine.mu window captures its definition: refreshed into the stage, or as
// the prepared drop's immutable observation.
func (p *DropProvider) CaptureIdentitySequence(relation *catalog.Relation, attribute int32, ref ids.ObjectRef) error {
	// Do not capture the sequence definition before its actual operation grant:
	// the incumbent refresh selects this provider at grant time, not by name.
	if err := p.Scope.AcquireSequence(ref, lock.AccessExclusive, false); err != nil {
		return err
	}
	e := p.Owner
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if p.Stage == nil {
		capture, err := e.captureIdentityDropDefinitionLocked(p, relation, attribute, ref)
		p.IdentityDrop = capture
		return err
	}
	if err := e.RefreshIdentitySequenceLocked(p.Scope, p.Stage, relation, attribute, ref.Address); err != nil {
		return err
	}
	p.Base = p.Stage.Base
	p.IdentityDrop = identityDropCapture{Sequence: ref, Original: e.Catalog.State.Get(ref.Address)}
	p.Stage.SequenceState().ObserveIdentityDependencies(session.DependencyObservation{e.Catalog.State, []ids.ObjectAddress{ref.Address}})
	return nil
}

func (p *DropProvider) TombstoneIdentity(address ids.ObjectAddress) {
	if p.Stage != nil {
		ApplyIdentityDropTombstone(p.Stage, address)
	}
}

// The caller holds Engine.mu after the exact sequence's AX grant. This captures
// existing immutable definitions only: no generator, scalar or stage is touched.
func (e *Engine) captureIdentityDropDefinitionLocked(provider *DropProvider, relation *catalog.Relation, attribute int32, ref ids.ObjectRef) (identityDropCapture, error) {
	var capture identityDropCapture
	fresh, err := e.AuthorizationLocked(provider.Scope.Op)
	if err != nil {
		return capture, err
	}
	if fresh.Catalog.Epoch() != provider.Base.Epoch() || provider.Removed(ref.Address) {
		return capture, ids.ErrCatalogReplaced
	}
	descriptor := fresh.Catalog.Get(ref.Address)
	if staged := provider.Replacement(ref.Address); staged != nil {
		descriptor = staged
	}
	sequence, err := sequence.IdentityDefinition(descriptor, relation, attribute)
	if err != nil {
		return capture, err
	}
	// An earlier genuine observation is never overwritten by the refresh.
	observed := fresh.Catalog.Get(ref.Address)
	if !provider.Prepared.CaptureObservation(ref.Address, observed) {
		return capture, ids.ErrCatalogReplaced
	}
	provider.Prepared.Grants = append(provider.Prepared.Grants, ref)
	provider.Base = fresh.Catalog
	provider.Prepared.Discovery = fresh.Catalog
	provider.identitySource = sequence
	provider.Prepared.RecordEffect(table.DropEffect{IdentityRoots: []ids.ObjectAddress{ref.Address}, IdentityState: e.Catalog.State})
	return identityDropCapture{Sequence: ref, Original: e.Catalog.State.Get(ref.Address)}, nil
}

func ApplyIdentityDropTombstone(stage *CatalogStage, address ids.ObjectAddress) {
	// A TYPE before-command may have staged AS. The real tombstone wins over
	// that private descriptor/scalar without modifying any live generator.
	stage.ForgetDescriptor(address)
	stage.Sequence.DeleteGenerator(address)
}
