package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type physicalNotNullConstructor struct {
	before    *catalog.Relation
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
	current        *catalog.Relation
	repairs        []physicalNotNullRepair
	facts          []restoredPhysicalNotNullFact
	primary        *catalog.PhysicalPrimarySupport
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
	old, ok := auth.catalog.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation || old.Partition != nil || old.Membership != nil || created[ref.Address] != ref || inheritanceHasChildren(auth.catalog, old.Address()) || slices.ContainsFunc(old.UniqueConstraints, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
		return reject()
	}
	seen := make(map[int32]bool, len(repairs))
	for _, repair := range repairs {
		if repair.relationText != first.relationText || repair.primary != first.primary || !slices.Equal(repair.keys, first.keys) {
			return reject()
		}
		slot, exists := old.Index[repair.column]
		if !exists || !old.Columns[slot].Nullable || !old.Columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := old.Attributes[slot]
		if seen[attribute] || slices.ContainsFunc(old.NotNullConstraints, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute }) {
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
func (e *Engine) capturePhysicalNotNullConstructor(op *Operation, constructor *physicalNotNullConstructor, indexes map[ids.ObjectAddress]*catalog.Relation) (*restoredPhysicalNotNull, error) {
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
	current, ok := auth.catalog.Relation(constructor.ref.Address)
	if !ok || current == constructor.before || current.Owner != constructor.before.Owner || current.RelationKind != catalog.TableRelation || current.Partition != nil || current.Membership != nil {
		return reject()
	}
	if constructor.deferred != nil {
		return nil, validateDeferredPhysicalPrimary(constructor, current)
	}
	pair := &restoredPhysicalNotNull{op: op, authority: auth, ref: constructor.ref, current: current, repairs: constructor.repairs, facts: make([]restoredPhysicalNotNullFact, len(constructor.repairs))}
	for i, repair := range constructor.repairs {
		slot, exists := current.Index[repair.column]
		beforeSlot, beforeExists := constructor.before.Index[repair.column]
		if !exists || !beforeExists || current.Attributes[slot] != constructor.before.Attributes[beforeSlot] || current.Columns[slot].Usage != constructor.before.Columns[beforeSlot].Usage || current.Columns[slot].Nullable || !current.Columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := current.Attributes[slot]
		index := slices.IndexFunc(current.NotNullConstraints, func(c catalog.NotNullConstraint) bool {
			return c.Attribute == attribute && c.OID != 0 && !c.NotValid && !c.NoInherit
		})
		if index < 0 {
			return reject()
		}
		if graph := current.Inheritance; graph != nil {
			fact := graph.NotNull[attribute]
			if !fact.Local || fact.Count() != 0 {
				return reject()
			}
		}
		pair.facts[i] = restoredPhysicalNotNullFact{attribute: attribute, oid: current.NotNullConstraints[index].OID, usage: current.Columns[slot].Usage}
		primary := physicalPrimaryForAttribute(current, attribute)
		if repair.primary == "" {
			if slices.ContainsFunc(current.UniqueConstraints, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
				return reject()
			}
		} else {
			if primary == nil || primary.Name != repair.primary || len(primary.Attributes) != len(repair.keys) {
				return reject()
			}
			for k, key := range primary.Attributes {
				if current.Columns[current.IndexForAttribute(key)].Name != repair.keys[k] {
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
			} else if primary.OID != pair.primary.Constraint || primary.Index != pair.primary.Index {
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
	if !partitionConstraintReplayImport(e, op, auth) || auth.roleRecord != pair.authority.roleRecord || auth.catalog.Epoch() != pair.ref.Epoch || auth.catalog.Get(pair.ref.Address) != pair.current {
		e.mu.Unlock()
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, repair.relation, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old := pair.current
	if ref != pair.ref || old.Partition != nil || old.Membership != nil || inheritanceHasChildren(auth.catalog, old.Address()) {
		e.mu.Unlock()
		return reject()
	}
	fact := pair.facts[pair.position]
	slot := old.IndexForAttribute(fact.attribute)
	index := slices.IndexFunc(old.NotNullConstraints, func(c catalog.NotNullConstraint) bool {
		return c.OID == fact.oid && c.Attribute == fact.attribute && !c.NotValid && !c.NoInherit
	})
	if slot < 0 || old.Columns[slot].Name != repair.column || old.Columns[slot].Usage != fact.usage || old.Columns[slot].Nullable || !old.Columns[slot].NullabilityKnown || index < 0 {
		e.mu.Unlock()
		return reject()
	}
	if graph := old.Inheritance; graph != nil {
		provenance := graph.NotNull[fact.attribute]
		if !provenance.Local || provenance.Count() != 0 {
			e.mu.Unlock()
			return reject()
		}
	}
	primary := physicalPrimaryForAttribute(old, fact.attribute)
	if pair.primary == nil {
		if primary != nil || slices.ContainsFunc(old.UniqueConstraints, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
			e.mu.Unlock()
			return reject()
		}
	} else if !physicalPrimarySupportValid(auth.catalog, old, primary, pair.primary) {
		e.mu.Unlock()
		return reject()
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.NotNullConstraints = slices.Clone(old.NotNullConstraints)
	if old.Inheritance != nil {
		next.Inheritance = cloneInheritance(old.Inheritance)
	}
	removeDetachedNotNullMetadata(&next, old.NotNullConstraints[index])
	cause := &catalog.PhysicalNotNullCause{Kind: catalog.PhysicalNotNullImportFlag, Ref: ref, Origin: ref, Attribute: fact.attribute, RemovedNN: fact.oid, Usage: fact.usage}
	if pair.primary != nil {
		cause.Kind = catalog.PhysicalNotNullImportCleanup
		cause.Primary = pair.primary.Constraint
		cause.Index = pair.primary.Index
	}
	cause.Self = cause
	next.PhysicalNotNull = maps.Clone(old.PhysicalNotNull)
	if next.PhysicalNotNull == nil {
		next.PhysicalNotNull = make(map[int32]catalog.PhysicalNotNullProof)
	}
	next.PhysicalNotNull[fact.attribute] = catalog.PhysicalNotNullProof{Cause: cause, Usage: fact.usage, Support: pair.primary}
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
