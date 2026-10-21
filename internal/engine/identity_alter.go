package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// identityCandidate applies ADD, SET or DROP identity. SET acquires the
// identity sequence between the table decisions; ADD stages under Engine.mu.
func (e *Engine) identityCandidate(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	switch action.Action {
	case parser.DropIdentity:
		next, _, err := e.dropIdentityDefinition(stageDropProvider(scope.op.Context(), stage, scope, nil, nil), old, action)
		return next, err
	case parser.AddIdentity:
		h := ddlHost{e: e, stage: stage}
		if scope != nil {
			h.op = scope.op
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		return table.AddIdentityCandidate(h, auth, old, action)
	}
	attribute, identity, err := table.PrepareIdentitySet(old, action)
	if err != nil {
		return old, err
	}
	if options := action.Identity.Options; len(options) != 0 {
		ref := e.catalog.Domain.Ref(stage.Base.Epoch(), identity.Sequence)
		if err := scope.acquireSequence(ref, lock.ShareRowExclusive, false); err != nil {
			return old, err
		}
		e.mu.Lock()
		err := (alterHost{ddlHost{e: e, op: scope.op, stage: stage}, scope}).StageIdentitySequenceOptions(old, attribute, identity.Sequence, options)
		e.mu.Unlock()
		if err != nil {
			return old, err
		}
	}
	return table.FinishIdentitySet(old, attribute, identity, action)
}

// Both identity SET and TYPE AS use the sequence owner's migration/state rules.
// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) stageIdentitySequenceOptionsLocked(scope *StatementScope, stage *CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress, raw []parser.SequenceOption) error {
	if err := e.refreshIdentitySequenceLocked(scope, stage, table, attribute, address); err != nil {
		return err
	}
	if _, removed := stage.Removal(address); removed {
		return ids.ErrCatalogReplaced
	}
	descriptor := stage.Get(address)
	if descriptor == nil {
		descriptor = stage.Base.Get(address)
	}
	relation, ok := descriptor.(*catalog.Relation)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	generator := stage.sequence.Generator(address)
	if generator == nil {
		generator = e.sequenceGeneratorLocked(scope, address)
	}
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	options, restart, err := sequence.BindOptions(relation.Sequence, *generator, raw)
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	changed := *relation
	changed.Sequence = &options
	changed.ObjectRevision = revision
	changed.SequencePlanStamp = revision
	if stage.Base.Get(changed.Address()) == nil {
		stage.UpdateDescriptor(&changed)
	} else if err := stage.Replace(&changed); err != nil {
		return err
	}
	stage.sequenceState().SetGenerator(address, sequence.ReplacementGenerator(*generator, revision, restart))
	return nil
}

func (e *Engine) alterIdentityTypeSequence(scope *StatementScope, stage *CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress, usage sqltype.Usage) error {
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), address)
	if err := scope.acquireSequence(ref, lock.ShareRowExclusive, false); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	as, err := sequence.IdentityTypeOption(usage.Type.ID())
	if err != nil {
		return err
	}
	if err := e.stageIdentitySequenceOptionsLocked(scope, stage, table, attribute, address, []parser.SequenceOption{as}); err != nil {
		return err
	}
	// AS discards this backend's tail even if a later table pass rolls back.
	// currval/lastval and unseen peer caches are physical-session authorities.
	if memory := payloadOf(scope.op.Session).sequences.Memory(address); memory != nil {
		memory.DiscardTailLocked()
	}
	return nil
}

// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) refreshIdentitySequenceLocked(scope *StatementScope, stage *CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress) error {
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	if fresh.Catalog.Epoch() != stage.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	descriptor := fresh.Catalog.Get(address)
	if staged, exists := stage.Find(address); exists {
		descriptor = staged
	}
	if _, err := sequence.IdentityDefinition(descriptor, table, attribute); err != nil {
		return err
	}
	stage.sequenceState().CaptureIdentityOriginal(address, e.catalog.state.Get(address))
	stage.Base = fresh.Catalog
	return nil
}
