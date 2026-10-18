package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type physicalNotNullConstructor struct {
	before    *relationDescriptor
	ref       ids.ObjectRef
	repairs   []physicalNotNullRepair
	indexName string
	deferred  *parser.UniqueConstraint
	competing bool
}
type restoredPhysicalNotNullFact struct {
	attribute int32
	oid       ids.OID
	usage     sqltype.Usage
}
type restoredPhysicalNotNull struct {
	op             *Operation
	authority      AuthorizationView
	ref            ids.ObjectRef
	current        *relationDescriptor
	repairs        []physicalNotNullRepair
	facts          []restoredPhysicalNotNullFact
	primary        *physicalPrimarySupport
	position       int
	ownerDirective bool
}

// Look ahead only across the optional owner-to-superuser directive and a closed
// sequence of canonical cleanups. An ordinary ALTER receives no extra authority.
func physicalNotNullFollowing(statements []parser.Statement, position int) []physicalNotNullRepair {
	position++
	if position < len(statements) {
		if directive, ok := statements[position].(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
			position++
		}
	}
	var repairs []physicalNotNullRepair
	for ; position < len(statements); position++ {
		deletion, ok := statements[position].(*parser.Delete)
		if !ok {
			break
		}
		repair, valid := restorePhysicalNotNullSyntax(deletion)
		if !valid {
			break
		}
		repairs = append(repairs, repair)
	}
	return repairs
}

func (e *Engine) preparePhysicalNotNullConstructor(op *Operation, raw *parser.AlterTable, repairs []physicalNotNullRepair, created map[ids.ObjectAddress]ids.ObjectRef) (*physicalNotNullConstructor, error) {
	if len(repairs) == 0 {
		return nil, nil
	}
	reject := func() (*physicalNotNullConstructor, error) {
		return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	if raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 {
		return reject()
	}
	first := repairs[0]
	if raw.Name.Parts[0].Text != first.relation.Parts[0].Text || raw.Name.Parts[1].Text != first.relation.Parts[1].Text {
		return reject()
	}
	action := raw.Actions[0]
	if first.primary == "" {
		if !raw.Only || len(repairs) != 1 || action.Action != parser.SetNotNull || action.Column.Text != first.column || action.NoInherit || action.NotValid {
			return reject()
		}
	} else if action.Action == parser.AddUsingIndex {
		if action.UsingIndex == nil || !action.UsingIndex.Primary || action.UsingIndex.Name.Text != first.primary || action.UsingIndex.Index.Text == "" {
			return reject()
		}
	} else {
		if !raw.Only || action.Action != parser.AddUnique || action.Unique == nil || !action.Unique.Primary || action.Unique.NullsNotDistinct || action.Unique.Name.Text != first.primary || len(action.Unique.Columns) != len(first.keys) {
			return reject()
		}
		for i, column := range action.Unique.Columns {
			if column.Text != first.keys[i] {
				return reject()
			}
		}
	}
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	old, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || old.relationKind != tableRelation || old.partition != nil || old.membership != nil || created[ref.Address] != ref || inheritanceHasChildren(auth.catalog, old.address) || slices.ContainsFunc(old.uniqueConstraints, func(c uniqueConstraint) bool { return c.Primary }) {
		return reject()
	}
	seen := make(map[int32]bool, len(repairs))
	for _, repair := range repairs {
		if repair.relationText != first.relationText || repair.primary != first.primary || !slices.Equal(repair.keys, first.keys) {
			return reject()
		}
		slot, exists := old.index[repair.column]
		if !exists || !old.columns[slot].Nullable || !old.columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := old.attributes[slot]
		if seen[attribute] || slices.ContainsFunc(old.notNullConstraints, func(c notNullConstraint) bool { return c.Attribute == attribute }) {
			return reject()
		}
		seen[attribute] = true
	}
	constructor := &physicalNotNullConstructor{before: old, ref: ref, repairs: repairs}
	if action.Action == parser.AddUsingIndex {
		constructor.indexName = action.UsingIndex.Index.Text
	}
	return constructor, nil
}

// Capture actual normal constructor results, not a generated-name prediction.
func (e *Engine) capturePhysicalNotNullConstructor(op *Operation, constructor *physicalNotNullConstructor, indexes map[ids.ObjectAddress]*relationDescriptor) (*restoredPhysicalNotNull, error) {
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	reject := func() (*restoredPhysicalNotNull, error) {
		return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	if !partitionConstraintReplayImport(e, op, auth) || constructor.ref.Epoch != auth.catalog.Epoch() {
		return reject()
	}
	current, ok := auth.catalog.state.descriptors[constructor.ref.Address].(*relationDescriptor)
	if !ok || current == constructor.before || current.owner != constructor.before.owner || current.relationKind != tableRelation || current.partition != nil || current.membership != nil {
		return reject()
	}
	if constructor.deferred != nil {
		return nil, validateDeferredPhysicalPrimary(constructor, current)
	}
	pair := &restoredPhysicalNotNull{op: op, authority: auth, ref: constructor.ref, current: current, repairs: constructor.repairs, facts: make([]restoredPhysicalNotNullFact, len(constructor.repairs))}
	for i, repair := range constructor.repairs {
		slot, exists := current.index[repair.column]
		beforeSlot, beforeExists := constructor.before.index[repair.column]
		if !exists || !beforeExists || current.attributes[slot] != constructor.before.attributes[beforeSlot] || current.columns[slot].Usage != constructor.before.columns[beforeSlot].Usage || current.columns[slot].Nullable || !current.columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := current.attributes[slot]
		index := slices.IndexFunc(current.notNullConstraints, func(c notNullConstraint) bool {
			return c.Attribute == attribute && c.OID != 0 && !c.NotValid && !c.NoInherit
		})
		if index < 0 {
			return reject()
		}
		if graph := current.inheritance; graph != nil {
			fact := graph.notNull[attribute]
			if !fact.local || fact.count() != 0 {
				return reject()
			}
		}
		pair.facts[i] = restoredPhysicalNotNullFact{attribute: attribute, oid: current.notNullConstraints[index].OID, usage: current.columns[slot].Usage}
		primary := physicalPrimaryForAttribute(current, attribute)
		if repair.primary == "" {
			if slices.ContainsFunc(current.uniqueConstraints, func(c uniqueConstraint) bool { return c.Primary }) {
				return reject()
			}
		} else {
			if primary == nil || primary.Name != repair.primary || len(primary.Attributes) != len(repair.keys) {
				return reject()
			}
			for k, key := range primary.Attributes {
				if current.columns[current.indexForAttribute(key)].Name != repair.keys[k] {
					return reject()
				}
			}
			if pair.primary == nil {
				pair.primary = capturePhysicalPrimarySupport(current, primary)
				if !physicalPrimarySupportValid(auth.catalog, current, primary, pair.primary) {
					return reject()
				}
				if constructor.indexName != "" && !physicalImportedIndexMatches(indexes[primary.Index], constructor, current, primary) {
					return reject()
				}
			} else if primary.OID != pair.primary.constraint || primary.Index != pair.primary.index {
				return reject()
			}
		}
	}
	return pair, nil
}

func (pair *restoredPhysicalNotNull) admits(stmt parser.Statement) bool {
	if _, ok := stmt.(*parser.Delete); ok {
		return true
	}
	if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && !pair.ownerDirective {
		pair.ownerDirective = true
		return true
	}
	return false
}

// The selected NN and its outbound dependency projection disappear together in
// the sole private stage. The surviving physical flag is not a synthetic NN row.
func (e *Engine) restorePhysicalNotNull(op *Operation, raw *parser.Delete, pair *restoredPhysicalNotNull) error {
	repair, valid := restorePhysicalNotNullSyntax(raw)
	reject := func() error { return sqlError("0A000", restorePhysicalNotNullBoundary) }
	if !valid || pair == nil || pair.op != op || pair.position >= len(pair.repairs) || !samePhysicalRepair(repair, pair.repairs[pair.position]) {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_constraint")
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.roleRecord != pair.authority.roleRecord || auth.catalog.Epoch() != pair.ref.Epoch || auth.catalog.state.descriptors[pair.ref.Address] != pair.current {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old := pair.current
	if ref != pair.ref || old.partition != nil || old.membership != nil || inheritanceHasChildren(auth.catalog, old.address) {
		e.mu.Unlock()
		return reject()
	}
	fact := pair.facts[pair.position]
	slot := old.indexForAttribute(fact.attribute)
	index := slices.IndexFunc(old.notNullConstraints, func(c notNullConstraint) bool {
		return c.OID == fact.oid && c.Attribute == fact.attribute && !c.NotValid && !c.NoInherit
	})
	if slot < 0 || old.columns[slot].Name != repair.column || old.columns[slot].Usage != fact.usage || old.columns[slot].Nullable || !old.columns[slot].NullabilityKnown || index < 0 {
		e.mu.Unlock()
		return reject()
	}
	if graph := old.inheritance; graph != nil {
		provenance := graph.notNull[fact.attribute]
		if !provenance.local || provenance.count() != 0 {
			e.mu.Unlock()
			return reject()
		}
	}
	primary := physicalPrimaryForAttribute(old, fact.attribute)
	if pair.primary == nil {
		if primary != nil || slices.ContainsFunc(old.uniqueConstraints, func(c uniqueConstraint) bool { return c.Primary }) {
			e.mu.Unlock()
			return reject()
		}
	} else if !physicalPrimarySupportValid(auth.catalog, old, primary, pair.primary) {
		e.mu.Unlock()
		return reject()
	}
	if old.revision == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.revision++
	next.notNullConstraints = slices.Clone(old.notNullConstraints)
	if old.inheritance != nil {
		next.inheritance = cloneInheritance(old.inheritance)
	}
	removeDetachedNotNullMetadata(&next, old.notNullConstraints[index])
	cause := &physicalNotNullCause{kind: physicalNotNullImportFlag, ref: ref, origin: ref, attribute: fact.attribute, removedNN: fact.oid, usage: fact.usage}
	if pair.primary != nil {
		cause.kind = physicalNotNullImportCleanup
		cause.primary = pair.primary.constraint
		cause.index = pair.primary.index
	}
	cause.self = cause
	next.physicalNotNull = maps.Clone(old.physicalNotNull)
	if next.physicalNotNull == nil {
		next.physicalNotNull = make(map[int32]physicalNotNullProof)
	}
	next.physicalNotNull[fact.attribute] = physicalNotNullProof{cause: cause, usage: fact.usage, support: pair.primary}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	if _, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return err
	}
	pair.current = &next
	pair.position++
	return nil
}
