package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// RestoredPhysicalNotNull is the receipt of one completed physical NOT NULL
// constructor: the relation it left, the NOT NULL facts its cleanup
// repairs remove in order, and the primary key support, if any. It lives
// only inside one isolated Restore operation and role record.
type RestoredPhysicalNotNull struct {
	Op         *session.Operation
	RoleRecord *access.RoleRecord
	Ref        ids.ObjectRef
	Current    *catalog.Relation
	Repairs    []table.PhysicalNotNullRepair
	Facts      []table.RestoredPhysicalNotNullFact
	Primary    *catalog.PhysicalPrimarySupport
	// Position is the next repair to apply.
	Position int
	// OwnerDirective is set once the one owner directive between the
	// repairs has been admitted.
	OwnerDirective bool
}

// Admits reports whether stmt may run while the cleanup repairs are
// pending: a repair DELETE, or the first non-default owner directive.
func (pair *RestoredPhysicalNotNull) Admits(stmt parser.Statement) bool {
	if _, ok := stmt.(*parser.Delete); ok {
		return true
	}
	if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && !pair.OwnerDirective {
		pair.OwnerDirective = true
		return true
	}
	return false
}

// Holds reports whether view, read under roles, still holds the relation
// the receipt last recorded.
func (pair *RestoredPhysicalNotNull) Holds(roles *access.RoleRecord, view catalog.Read) bool {
	return roles == pair.RoleRecord && view.Get(pair.Ref.Address) == pair.Current
}

// Pairs reports whether repair, issued by op, is the next pending repair.
func (pair *RestoredPhysicalNotNull) Pairs(op *session.Operation, repair table.PhysicalNotNullRepair) bool {
	return pair != nil && pair.Op == op && pair.Position < len(pair.Repairs) && table.SamePhysicalRepair(repair, pair.Repairs[pair.Position])
}

// Advance records next, the published result of the pending repair.
func (pair *RestoredPhysicalNotNull) Advance(next *catalog.Relation) {
	pair.Current = next
	pair.Position++
}

// PhysicalNotNullConstructorShape reports whether raw is the constructor
// that repairs, the nonempty run of cleanup repairs following it, expect:
// a SET NOT NULL, ADD PRIMARY KEY or ADD PRIMARY KEY USING INDEX on their
// relation.
func PhysicalNotNullConstructorShape(raw *parser.AlterTable, repairs []table.PhysicalNotNullRepair) bool {
	if raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 {
		return false
	}
	first := repairs[0]
	if raw.Name.Parts[0].Text != first.Relation.Parts[0].Text || raw.Name.Parts[1].Text != first.Relation.Parts[1].Text {
		return false
	}
	action := raw.Actions[0]
	if first.Primary == "" {
		return raw.Only && len(repairs) == 1 && action.Action == parser.SetNotNull && action.Column.Text == first.Column && !action.NoInherit && !action.NotValid
	}
	if action.Action == parser.AddUsingIndex {
		return action.UsingIndex != nil && action.UsingIndex.Primary && action.UsingIndex.Name.Text == first.Primary && action.UsingIndex.Index.Text != ""
	}
	if !raw.Only || action.Action != parser.AddUnique || action.Unique == nil || !action.Unique.Primary || action.Unique.NullsNotDistinct || action.Unique.Name.Text != first.Primary || len(action.Unique.Columns) != len(first.Keys) {
		return false
	}
	for i, column := range action.Unique.Columns {
		if column.Text != first.Keys[i] {
			return false
		}
	}
	return true
}

// PreparePhysicalNotNullConstructor returns the constructor of raw on ref, a
// childless ordinary table this Restore created, with no primary key and no
// NOT NULL on any repaired column yet.
func PreparePhysicalNotNullConstructor(view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable, repairs []table.PhysicalNotNullRepair, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	reject := func() (*table.PhysicalNotNullConstructor, error) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	old, ok := view.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation || old.Partition != nil || old.Membership != nil || created[ref.Address] != ref || catalog.InheritanceHasChildren(view, old.Address()) || slices.ContainsFunc(old.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
		return reject()
	}
	first := repairs[0]
	seen := make(map[int32]bool, len(repairs))
	for _, repair := range repairs {
		if repair.RelationText != first.RelationText || repair.Primary != first.Primary || !slices.Equal(repair.Keys, first.Keys) {
			return reject()
		}
		slot, exists := old.Index[repair.Column]
		if !exists || !old.Columns[slot].Nullable || !old.Columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := old.Attributes[slot]
		if seen[attribute] || slices.ContainsFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute }) {
			return reject()
		}
		seen[attribute] = true
	}
	constructor := &table.PhysicalNotNullConstructor{Before: old, Ref: ref, Repairs: repairs}
	if action := raw.Actions[0]; action.Action == parser.AddUsingIndex {
		constructor.IndexName = action.UsingIndex.Index.Text
	}
	return constructor, nil
}

// CapturePhysicalNotNullConstructor records the actual result of the
// completed constructor, not a generated-name prediction: one valid,
// inheritable, purely local NOT NULL per repaired column and, for a primary
// key constructor, its key support. indexes holds the indexes this Restore
// created by name.
func CapturePhysicalNotNullConstructor(op *session.Operation, roles *access.RoleRecord, view catalog.Read, constructor *table.PhysicalNotNullConstructor, indexes map[ids.ObjectAddress]*catalog.Relation) (*RestoredPhysicalNotNull, error) {
	reject := func() (*RestoredPhysicalNotNull, error) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	current, ok := view.Relation(constructor.Ref.Address)
	if !ok || current == constructor.Before || current.Owner != constructor.Before.Owner || current.RelationKind != catalog.TableRelation || current.Partition != nil || current.Membership != nil {
		return reject()
	}
	if constructor.Deferred != nil {
		return nil, table.ValidateDeferredPhysicalPrimary(constructor, current)
	}
	pair := &RestoredPhysicalNotNull{Op: op, RoleRecord: roles, Ref: constructor.Ref, Current: current, Repairs: constructor.Repairs, Facts: make([]table.RestoredPhysicalNotNullFact, len(constructor.Repairs))}
	for i, repair := range constructor.Repairs {
		slot, exists := current.Index[repair.Column]
		beforeSlot, beforeExists := constructor.Before.Index[repair.Column]
		if !exists || !beforeExists || current.Attributes[slot] != constructor.Before.Attributes[beforeSlot] || current.Columns[slot].Usage != constructor.Before.Columns[beforeSlot].Usage || current.Columns[slot].Nullable || !current.Columns[slot].NullabilityKnown {
			return reject()
		}
		attribute := current.Attributes[slot]
		idx := slices.IndexFunc(current.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
			return c.Attribute == attribute && c.OID != 0 && !c.NotValid && !c.NoInherit
		})
		if idx < 0 {
			return reject()
		}
		if graph := current.Inheritance; graph != nil {
			fact := graph.NotNull[attribute]
			if !fact.Local || fact.Count() != 0 {
				return reject()
			}
		}
		pair.Facts[i] = table.RestoredPhysicalNotNullFact{Attribute: attribute, OID: current.Constraints.NotNull[idx].OID, Usage: current.Columns[slot].Usage}
		primary := catalog.PhysicalPrimaryForAttribute(current, attribute)
		if repair.Primary == "" {
			if slices.ContainsFunc(current.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
				return reject()
			}
		} else {
			if primary == nil || primary.Name != repair.Primary || len(primary.Attributes) != len(repair.Keys) {
				return reject()
			}
			for k, key := range primary.Attributes {
				if current.Columns[current.IndexForAttribute(key)].Name != repair.Keys[k] {
					return reject()
				}
			}
			if pair.Primary == nil {
				pair.Primary = catalog.CapturePhysicalPrimarySupport(current, primary)
				if !index.PhysicalPrimarySupportValid(view, current, primary, pair.Primary) {
					return reject()
				}
				if constructor.IndexName != "" && !table.PhysicalImportedIndexMatches(indexes[primary.Index], constructor, current, primary) {
					return reject()
				}
			} else if primary.OID != pair.Primary.Constraint || primary.Index != pair.Primary.Index {
				return reject()
			}
		}
	}
	return pair, nil
}

// PhysicalNotNullCleanup returns pair's relation with the pending repair's
// NOT NULL and its outbound dependency projection removed together, leaving
// a physical NOT NULL flag whose cause is this import; repair names that
// NOT NULL's column on ref. The surviving flag is not a synthetic NOT NULL.
func PhysicalNotNullCleanup(view catalog.Read, pair *RestoredPhysicalNotNull, ref ids.ObjectRef, repair table.PhysicalNotNullRepair) (*catalog.Relation, error) {
	reject := func() (*catalog.Relation, error) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	old := pair.Current
	if ref != pair.Ref || old.Partition != nil || old.Membership != nil || catalog.InheritanceHasChildren(view, old.Address()) {
		return reject()
	}
	fact := pair.Facts[pair.Position]
	slot := old.IndexForAttribute(fact.Attribute)
	idx := slices.IndexFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
		return c.OID == fact.OID && c.Attribute == fact.Attribute && !c.NotValid && !c.NoInherit
	})
	if slot < 0 || old.Columns[slot].Name != repair.Column || old.Columns[slot].Usage != fact.Usage || old.Columns[slot].Nullable || !old.Columns[slot].NullabilityKnown || idx < 0 {
		return reject()
	}
	if graph := old.Inheritance; graph != nil {
		provenance := graph.NotNull[fact.Attribute]
		if !provenance.Local || provenance.Count() != 0 {
			return reject()
		}
	}
	primary := catalog.PhysicalPrimaryForAttribute(old, fact.Attribute)
	if pair.Primary == nil {
		if primary != nil || slices.ContainsFunc(old.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
			return reject()
		}
	} else if !index.PhysicalPrimarySupportValid(view, old, primary, pair.Primary) {
		return reject()
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	if old.Inheritance != nil {
		next.Inheritance = catalog.CloneInheritance(old.Inheritance)
	}
	table.RemoveDetachedNotNullMetadata(&next, old.Constraints.NotNull[idx])
	cause := &catalog.PhysicalNotNullCause{Kind: catalog.PhysicalNotNullImportFlag, Ref: ref, Origin: ref, Attribute: fact.Attribute, RemovedNN: fact.OID, Usage: fact.Usage}
	if pair.Primary != nil {
		cause.Kind = catalog.PhysicalNotNullImportCleanup
		cause.Primary = pair.Primary.Constraint
		cause.Index = pair.Primary.Index
	}
	cause.Self = cause
	next.Constraints.PhysicalNotNull = maps.Clone(old.Constraints.PhysicalNotNull)
	if next.Constraints.PhysicalNotNull == nil {
		next.Constraints.PhysicalNotNull = make(map[int32]catalog.PhysicalNotNullProof)
	}
	next.Constraints.PhysicalNotNull[fact.Attribute] = catalog.PhysicalNotNullProof{Cause: cause, Usage: fact.Usage, Support: pair.Primary}
	return &next, nil
}
