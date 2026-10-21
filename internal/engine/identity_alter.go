package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) identityCandidate(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	if action.Action == parser.DropIdentity {
		next, _, err := e.dropIdentityDefinition(stageDropProvider(scope.op.Context(), stage, scope, nil, nil), old, action)
		return next, err
	}
	slot, ok := old.Index[action.Column.Text]
	if catalog.IsSystemColumn(action.Column.Text) {
		return old, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	if !ok {
		return old, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.Name()))
	}
	attribute := old.Attributes[slot]
	identity, exists := old.ColumnMeta.Identities[attribute]
	message := fmt.Sprintf(`column "%s" of relation "%s"`, action.Column.Text, old.Name())
	if action.Action == parser.AddIdentity {
		// PostgreSQL executes the sequence before-command before table guards.
		// Stage it privately; a failed guard must not change the old link.
		next := *old
		next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
		column := &parser.Column{Name: action.Column.Text, Constraints: []parser.ColumnConstraint{{Identity: action.Identity}}}
		e.mu.Lock()
		err := StageCreateIdentities(ddlHost{e: e, stage: stage}, auth, &next, &parser.CreateTable{Elements: []parser.TableElement{{Column: column}}})
		e.mu.Unlock()
		if err != nil {
			return old, err
		}
		if exists {
			return old, sqlError("55000", message+" is already an identity column")
		}
		if old.Columns[slot].Nullable {
			return old, sqlError("55000", message+" must be declared NOT NULL before identity can be added")
		}
		if _, ok := old.ColumnMeta.Defaults[attribute]; ok || old.ColumnMeta.Generations[attribute].Expression != nil {
			return old, sqlError("55000", message+" already has a default value")
		}
		return &next, nil
	}
	if !exists {
		return old, sqlError("55000", message+" is not an identity column")
	}
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), identity.Sequence)
	definition := action.Identity
	if len(definition.Options) != 0 {
		if err := sequence.CheckIdentitySetOptions(definition.Options); err != nil {
			return old, err
		}
		if err := scope.acquireSequence(ref, lock.ShareRowExclusive, false); err != nil {
			return old, err
		}
		e.mu.Lock()
		err := e.stageIdentitySequenceOptionsLocked(scope, stage, old, attribute, identity.Sequence, definition.Options)
		e.mu.Unlock()
		if err != nil {
			return old, err
		}
	}
	// Numeric before-command errors take precedence over a utility mode conflict.
	if definition.ConflictingModes {
		return old, sqlError("42601", "conflicting or redundant options")
	}
	next := *old
	next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
	if definition.Mode != parser.IdentityNone {
		identity.Mode = definition.Mode
		next.ColumnMeta.Identities[attribute] = identity
	}
	return &next, nil
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
