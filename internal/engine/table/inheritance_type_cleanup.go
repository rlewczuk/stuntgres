package table

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Recursive TYPE drops dependent nonlocal facts when their actual parent TYPE
// did not reconstruct them. Preserve physical NN only from that parent's issued
// cleanup certificate; neither missing metadata nor a count alone issues it.
func ReleaseInheritedTypeConstraints(view catalog.Read, state *InheritanceAlterPlan, old *catalog.Relation, actions []parser.AlterTableAction) *catalog.Relation {
	if state == nil || !state.Descendant || old.Inheritance == nil {
		return old
	}
	affected := func(attribute int32) bool {
		for _, a := range actions {
			if a.Action == parser.AlterColumnType {
				if slot, exists := old.Index[a.Column.Text]; exists && old.Attributes[slot] == attribute {
					return true
				}
			}
		}
		return false
	}
	typed := func(parent ids.ObjectAddress) bool {
		if !state.Selected[parent] {
			return false
		}
		return slices.ContainsFunc(state.Events[parent], func(event InheritanceAlterEvent) bool { return event.Action.Action == parser.AlterColumnType })
	}
	var next *catalog.Relation
	detach := func() {
		if next != nil {
			return
		}
		copy := *old
		copy.Inheritance = catalog.CloneInheritance(old.Inheritance)
		copy.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
		copy.Constraints.Check = slices.Clone(old.Constraints.Check)
		copy.Constraints.PhysicalNotNull = maps.Clone(old.Constraints.PhysicalNotNull)
		next = &copy
	}
	for _, fact := range old.Constraints.NotNull {
		p := old.Inheritance.NotNull[fact.Attribute]
		if !affected(fact.Attribute) || p.Local || len(p.Retired) != 0 || len(p.Origins) == 0 || fact.NotValid || fact.NoInherit {
			continue
		}
		var source *catalog.PhysicalNotNullCause
		valid := true
		for _, origin := range p.Origins {
			parent, ok := view.Relation(origin.Parent)
			if !ok || !typed(origin.Parent) || slices.ContainsFunc(parent.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.OID == origin.Constraint }) {
				valid = false
				break
			}
			proof := parent.Constraints.PhysicalNotNull[origin.Attribute]
			if !catalog.PhysicalNotNullStandaloneCause(proof.Cause) || proof.Cause.RemovedNN != origin.Constraint {
				valid = false
				break
			}
			source = proof.Cause
		}
		if !valid || source == nil {
			continue
		}
		detach()
		RemoveDetachedNotNullMetadata(next, fact)
		ref := view.Domain().Ref(view.Epoch(), old.Address())
		usage := old.Columns[old.IndexForAttribute(fact.Attribute)].Usage
		cause := &catalog.PhysicalNotNullCause{Kind: catalog.PhysicalNotNullInheritedTypeCleanup, Ref: ref, Origin: ref, Attribute: fact.Attribute, RemovedNN: fact.OID, Usage: usage, Source: source}
		cause.Self = cause
		if next.Constraints.PhysicalNotNull == nil {
			next.Constraints.PhysicalNotNull = make(map[int32]catalog.PhysicalNotNullProof)
		}
		next.Constraints.PhysicalNotNull[fact.Attribute] = catalog.PhysicalNotNullProof{Cause: cause, Usage: usage}
	}
	for _, fact := range old.Constraints.Check {
		p := old.Inheritance.Checks[fact.Name]
		if p.Local || len(p.Retired) != 0 || len(p.Origins) == 0 {
			continue
		}
		touches := slices.ContainsFunc(fact.Dependencies, func(d catalog.ExpressionDependency) bool {
			return d.Ref.Address.ClassID == ids.RelationClassID && d.Ref.Address.ObjectID == old.Address().ObjectID && affected(d.Ref.Address.SubID)
		})
		if !touches {
			continue
		}
		gone := true
		for _, origin := range p.Origins {
			parent, ok := view.Relation(origin.Parent)
			if !ok || !typed(origin.Parent) || slices.ContainsFunc(parent.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.OID == origin.Constraint }) {
				gone = false
				break
			}
		}
		if gone {
			detach()
			next.Constraints.Check = slices.DeleteFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.OID == fact.OID })
			delete(next.Inheritance.Checks, fact.Name)
		}
	}
	if next != nil {
		return next
	}
	return old
}
