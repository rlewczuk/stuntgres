package tablecmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// identityCandidate applies ADD, SET or DROP identity. SET acquires the
// identity sequence between the table decisions; ADD stages under Engine.mu.
func identityCandidate(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stage *core.CatalogStage, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	switch action.Action {
	case parser.DropIdentity:
		return table.DropIdentity(core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil), old, action)
	case parser.AddIdentity:
		h := TableHost{core.DDLHost{E: e, CatalogStage: stage}}
		if scope != nil {
			h.Op = scope.Op
		}
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return table.AddIdentityCandidate(h, auth, old, action)
	}
	attribute, identity, err := table.PrepareIdentitySet(old, action)
	if err != nil {
		return old, err
	}
	if options := action.Identity.Options; len(options) != 0 {
		ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), identity.Sequence)
		if err := scope.AcquireSequence(ref, lock.ShareRowExclusive, false); err != nil {
			return old, err
		}
		e.Mu.Lock()
		err := (AlterHost{TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, scope}).StageIdentitySequenceOptions(old, attribute, identity.Sequence, options)
		e.Mu.Unlock()
		if err != nil {
			return old, err
		}
	}
	return table.FinishIdentitySet(old, attribute, identity, action)
}

// Both identity SET and TYPE AS use the sequence owner's migration/state rules.
// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func stageIdentitySequenceOptionsLocked(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress, raw []parser.SequenceOption) error {
	if err := e.RefreshIdentitySequenceLocked(scope, stage, table, attribute, address); err != nil {
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
	generator := stage.Sequence.Generator(address)
	if generator == nil {
		generator = e.SequenceGeneratorLocked(scope, address)
	}
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	options, restart, err := sequence.BindOptions(relation.Sequence, *generator, raw)
	if err != nil {
		return err
	}
	revision, err := e.Catalog.RevisionLocked()
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
	stage.SequenceState().SetGenerator(address, sequence.ReplacementGenerator(*generator, revision, restart))
	return nil
}

func alterIdentityTypeSequence(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage, table *catalog.Relation, attribute int32, address ids.ObjectAddress, usage sqltype.Usage) error {
	ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), address)
	if err := scope.AcquireSequence(ref, lock.ShareRowExclusive, false); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	as, err := sequence.IdentityTypeOption(usage.Type.ID())
	if err != nil {
		return err
	}
	if err := stageIdentitySequenceOptionsLocked(e, scope, stage, table, attribute, address, []parser.SequenceOption{as}); err != nil {
		return err
	}
	// AS discards this backend's tail even if a later table pass rolls back.
	// currval/lastval and unseen peer caches are physical-session authorities.
	if memory := core.PayloadOf(scope.Op.Session).Sequences.Memory(address); memory != nil {
		memory.DiscardTailLocked()
	}
	return nil
}
