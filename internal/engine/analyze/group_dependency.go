package analyze

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// This state exists only while rewriting one ordinary GROUP BY. Source indexes
// are statement-local occurrence identities, never catalog relation identities.
type groupDependencyState struct {
	keys      []expr.Expr
	dependent []expr.Expr
	proofs    []plan.GroupDependency
	checked   map[int]bool
}

func (b *Binder) groupColumn(slot int) (QueryColumn, bool) {
	if b.Query.Namespace == nil {
		return QueryColumn{}, false
	}
	for _, qualifier := range b.Query.Namespace.Qualifiers {
		for _, column := range qualifier.Columns {
			if column.Slot == slot && column.Leaf >= 0 {
				return column, true
			}
		}
	}
	for _, column := range b.Query.Namespace.Columns {
		if column.Slot == slot && column.Leaf >= 0 {
			return column, true
		}
	}
	return QueryColumn{}, false
}

func (b *Binder) proveGroupOccurrence(occurrence int) bool {
	state := b.Grouping.dependency
	if state == nil || occurrence < 0 || occurrence >= len(b.Query.Sources) {
		return false
	}
	for _, proof := range state.proofs {
		if proof.Occurrence == occurrence {
			return true
		}
	}
	if state.checked[occurrence] {
		return false
	}
	if state.checked == nil {
		state.checked = make(map[int]bool)
	}
	state.checked[occurrence] = true
	leaf := &b.Query.Sources[occurrence]
	relation := leaf.Relation
	if relation == nil || (relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && relation.RelationKind != catalog.ForeignRelation) {
		return false
	}
	for _, key := range relation.Constraints.Unique {
		if !key.Primary || key.Deferrable || key.OID == 0 || len(key.Attributes) == 0 {
			continue
		}
		complete := true
		for _, attribute := range key.Attributes {
			found := false
			for _, expression := range state.keys {
				column, direct := expression.(plan.ColumnExpr)
				if !direct || column.Frame != b.FrameID() || column.Scope != 1 {
					continue
				}
				bound, ok := b.groupColumn(column.Slot)
				if ok && bound.Leaf == occurrence && bound.Physical >= 0 && bound.Physical < len(relation.Attributes) && relation.Attributes[bound.Physical] == attribute {
					found = true
					break
				}
			}
			complete = complete && found
		}
		if !complete {
			continue
		}
		b.captureDependency(leaf.Ref, relation.Revision(), catalog.DefinitionDependency)
		state.proofs = append(state.proofs, plan.GroupDependency{Parent: leaf.Ref, Constraint: ids.ObjectAddress{ClassID: 2606, ObjectID: key.OID}, Frame: b.FrameID(), Occurrence: occurrence, Attributes: slices.Clone(key.Attributes)})
		return true
	}
	return false
}

func (b *Binder) dependentGroupExpression(bound expr.Expr) (expr.Expr, bool) {
	if b.Grouping.dependency == nil {
		return nil, false
	}
	occurrence := -1
	switch e := bound.(type) {
	case plan.ColumnExpr:
		if e.Frame != b.FrameID() || e.Scope != 1 {
			return nil, false
		}
		if column, ok := b.groupColumn(e.Slot); ok {
			occurrence = column.Leaf
		}
	case plan.SystemAttributeExpr:
		if e.Frame != b.FrameID() || e.Scope != 1 || e.QueryOccurrence <= 0 {
			return nil, false
		}
		occurrence = e.QueryOccurrence - 1
		if occurrence >= len(b.Query.Sources) || b.Query.Sources[occurrence].Ref != e.Owner {
			return nil, false
		}
	case plan.RowExpr:
		// A whole row's presence belongs to its input producer. Retain the
		// constructor itself once, rather than reading that presence frame
		// above aggregation (or mistaking an absent row for an all-NULL row).
		if e.WholeNames == nil || e.PresenceSlot == 0 || e.PresenceFrame != b.FrameID() {
			return nil, false
		}
		for _, qualifier := range b.Query.Namespace.Qualifiers {
			if qualifier.Name == e.WholeName && qualifier.SystemOccurrence > 0 {
				occurrence = qualifier.SystemOccurrence - 1
				break
			}
		}
	default:
		return nil, false
	}
	if !b.proveGroupOccurrence(occurrence) {
		return nil, false
	}
	state := b.Grouping.dependency
	slot := 0
	for ; slot < len(state.dependent); slot++ {
		if b.SameGroupExpression(bound, state.dependent[slot]) {
			break
		}
	}
	if slot == len(state.dependent) {
		state.dependent = append(state.dependent, bound)
	}
	info := expr.InfoOf(bound)
	info.Property.RowDependent = false
	info.Property.StageDependent = true
	return plan.GroupExpr{Info: info, Frame: b.FrameID(), Slot: len(state.keys) + len(b.Grouping.Aggregates) + 1 + slot}, true
}
