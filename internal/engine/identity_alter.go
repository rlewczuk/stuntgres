package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) identityCandidate(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old *relationDescriptor, action parser.AlterTableAction) (*relationDescriptor, error) {
	if action.Action == parser.DropIdentity {
		next, _, err := e.dropIdentityDefinition(stageDropProvider(scope.op.Context(), stage, scope, nil, nil), old, action)
		return next, err
	}
	slot, ok := old.index[action.Column.Text]
	if isSystemColumn(action.Column.Text) {
		return old, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, action.Column.Text))
	}
	if !ok {
		return old, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, old.name))
	}
	attribute := old.attributes[slot]
	identity, exists := old.identities[attribute]
	message := fmt.Sprintf(`column "%s" of relation "%s"`, action.Column.Text, old.name)
	if action.Action == parser.AddIdentity {
		// PostgreSQL executes the sequence before-command before table guards.
		// Stage it privately; a failed guard must not change the old link.
		next := *old
		next.identities = maps.Clone(old.identities)
		column := &parser.Column{Name: action.Column.Text, Constraints: []parser.ColumnConstraint{{Identity: action.Identity}}}
		e.mu.Lock()
		err := e.stageCreateIdentities(auth, stage, &next, &parser.CreateTable{Elements: []parser.TableElement{{Column: column}}})
		e.mu.Unlock()
		if err != nil {
			return old, err
		}
		if exists {
			return old, sqlError("55000", message+" is already an identity column")
		}
		if old.columns[slot].Nullable {
			return old, sqlError("55000", message+" must be declared NOT NULL before identity can be added")
		}
		if _, ok := old.defaults[attribute]; ok || old.generations[attribute].expression != nil {
			return old, sqlError("55000", message+" already has a default value")
		}
		return &next, nil
	}
	if !exists {
		return old, sqlError("55000", message+" is not an identity column")
	}
	ref := e.catalog.domain.Ref(stage.base.Epoch(), identity.sequence)
	definition := action.Identity
	if len(definition.Options) != 0 {
		for _, option := range definition.Options {
			if option.Name == "logged" || option.Name == "unlogged" {
				return old, sqlError("XX000", fmt.Sprintf(`option "%s" not recognized`, option.Name))
			}
			if option.Name == "as" || option.Name == "owned" {
				return old, expr.ErrorAt("42601", fmt.Sprintf(`sequence option "%s" not supported here`, option.Name), option.Pos)
			}
		}
		if err := scope.acquireSequence(ref, shareRowExclusive, false); err != nil {
			return old, err
		}
		e.mu.Lock()
		err := e.stageIdentitySequenceOptionsLocked(scope, stage, old, attribute, identity.sequence, definition.Options)
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
	next.identities = maps.Clone(old.identities)
	if definition.Mode != parser.IdentityNone {
		identity.mode = definition.Mode
		next.identities[attribute] = identity
	}
	return &next, nil
}

// Both identity SET and TYPE AS use the sequence owner's migration/state rules.
// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) stageIdentitySequenceOptionsLocked(scope *StatementScope, stage *CatalogStage, table *relationDescriptor, attribute int32, address ids.ObjectAddress, raw []parser.SequenceOption) error {
	if err := e.refreshIdentitySequenceLocked(scope, stage, table, attribute, address); err != nil {
		return err
	}
	if _, removed := stage.removals[address]; removed {
		return ErrCatalogReplaced
	}
	descriptor := stage.descriptors[address]
	if descriptor == nil {
		descriptor = stage.base.Get(address)
	}
	sequence, ok := descriptor.(*relationDescriptor)
	if !ok {
		return ErrCatalogReplaced
	}
	generator := stage.generators[address]
	if generator == nil {
		generator = e.sequenceGeneratorLocked(scope, address)
	}
	if generator == nil {
		return fmt.Errorf("stuntgres: sequence state missing")
	}
	options, restart, err := bindSequenceOptions(sequence.sequence, *generator, raw)
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	changed := *sequence
	changed.sequence = &options
	changed.revision = revision
	changed.sequencePlanStamp = revision
	if stage.base.Get(changed.address) == nil {
		stage.descriptors[changed.address] = &changed
	} else if err := stage.Replace(&changed); err != nil {
		return err
	}
	replacement := *generator
	replacement.generation = revision
	if restart != nil {
		replacement.last = *restart
		replacement.called = false
	}
	if stage.generators == nil {
		stage.generators = make(map[ids.ObjectAddress]*sequenceGenerator)
	}
	stage.generators[address] = &replacement
	return nil
}

func (e *Engine) alterIdentityTypeSequence(scope *StatementScope, stage *CatalogStage, table *relationDescriptor, attribute int32, address ids.ObjectAddress, usage sqltype.Usage) error {
	ref := e.catalog.domain.Ref(stage.base.Epoch(), address)
	if err := scope.acquireSequence(ref, shareRowExclusive, false); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id := usage.Type.ID()
	if id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
		return sequenceOptionError("identity column type must be smallint, integer, or bigint")
	}
	_, _, name := sequenceTypeLimits(id)
	if err := e.stageIdentitySequenceOptionsLocked(scope, stage, table, attribute, address, []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: name}}}); err != nil {
		return err
	}
	// AS discards this backend's tail even if a later table pass rolls back.
	// currval/lastval and unseen peer caches are physical-session authorities.
	if memory := scope.op.session.sequences[address]; memory != nil {
		memory.remaining = 0
	}
	return nil
}

// Caller holds Engine.mu after the exact linked sequence's operation lock grant.
func (e *Engine) refreshIdentitySequenceLocked(scope *StatementScope, stage *CatalogStage, table *relationDescriptor, attribute int32, address ids.ObjectAddress) error {
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	if fresh.catalog.Epoch() != stage.base.Epoch() {
		return ErrCatalogReplaced
	}
	descriptor := fresh.catalog.Get(address)
	if staged, exists := stage.descriptors[address]; exists {
		descriptor = staged
	}
	if _, err := identitySequenceDefinition(descriptor, table, attribute); err != nil {
		return err
	}
	if stage.identityOriginals == nil {
		stage.identityOriginals = make(map[ids.ObjectAddress]Descriptor)
	}
	if _, captured := stage.identityOriginals[address]; !captured {
		stage.identityOriginals[address] = e.catalog.state.Get(address)
	}
	stage.base = fresh.catalog
	return nil
}
