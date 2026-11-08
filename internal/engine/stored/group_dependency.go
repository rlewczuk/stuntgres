package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func validateGroupConstraint(view catalog.Read, proof plan.GroupDependency) error {
	parent, ok := view.Relation(proof.Parent.Address)
	if !ok || proof.Parent.Address.SubID != 0 || proof.Parent.Domain() != view.Domain() || proof.Parent.Epoch == 0 || proof.Parent.Epoch > view.Epoch() || proof.Constraint.ClassID != 2606 || proof.Constraint.ObjectID == 0 || proof.Constraint.SubID != 0 || len(proof.Attributes) == 0 || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.PartitionedRelation && parent.RelationKind != catalog.ForeignRelation) {
		return fmt.Errorf("stuntgres: invalid grouping dependency parent")
	}
	for _, key := range parent.Constraints.Unique {
		if key.OID == proof.Constraint.ObjectID && key.Primary && !key.Deferrable && slices.Equal(key.Attributes, proof.Attributes) {
			for _, attribute := range proof.Attributes {
				if !parent.HasAttribute(attribute) {
					return fmt.Errorf("stuntgres: missing grouping dependency attribute")
				}
			}
			return nil
		}
	}
	return fmt.Errorf("stuntgres: invalid grouping dependency constraint")
}

// Slot coordinates refer to the aggregate's actual input, not output origins.
// A derived query is deliberately opaque to its enclosing query's proof.
func groupDependencySource(node plan.Node, occurrence, offset int) (*plan.SourceNode, int) {
	switch n := node.(type) {
	case *plan.SourceNode:
		if n.Occurrence == occurrence {
			return n, offset
		}
		return nil, 0
	case *plan.DerivedNode, *plan.AggregateNode, *plan.ProjectNode:
		return nil, 0
	case *plan.JoinNode:
		if source, start := groupDependencySource(n.Left, occurrence, offset); source != nil {
			return source, start
		}
		return groupDependencySource(n.Right, occurrence, offset+n.LeftWidth)
	default:
		left, _ := plan.QueryInputs(node)
		if left != nil {
			return groupDependencySource(left, occurrence, offset)
		}
	}
	return nil, 0
}

func validateGroupDependencyNode(view catalog.Read, definition *View, n *plan.AggregateNode, frame uint32) error {
	// Even without dependency metadata, the schema must contain exactly the
	// keys, call results and grouping occurrence, followed by retained payload.
	if n.DependentStart()+len(n.Dependent) != len(n.Schema()) {
		return fmt.Errorf("stuntgres: invalid dependent group layout")
	}
	if len(n.Dependent) == 0 && len(n.Dependencies) == 0 {
		return nil
	}
	if len(n.Dependent) == 0 || len(n.Dependencies) == 0 || len(n.Grouping) != len(n.Keys) {
		return fmt.Errorf("stuntgres: invalid dependent group layout")
	}
	for i, key := range n.Keys {
		if key == nil || n.Schema()[i].Usage != key.ResultUsage() {
			return fmt.Errorf("stuntgres: invalid dependent group key layout")
		}
	}
	for i, call := range n.Calls {
		if call.Provider == nil || n.Schema()[len(n.Keys)+i].Usage != call.Provider.ResultUsage() {
			return fmt.Errorf("stuntgres: invalid dependent group call layout")
		}
	}
	occurrence := n.Schema()[n.DependentStart()-1].Usage.Type
	if occurrence == nil || occurrence.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid dependent group occurrence layout")
	}
	if n.Sets != nil && (len(n.Sets) != 1 || len(n.Sets[0]) != len(n.Keys)) {
		return fmt.Errorf("stuntgres: invalid dependent grouping set")
	}
	if n.Sets != nil {
		for i := range n.Keys {
			if !slices.Contains(n.Sets[0], i) {
				return fmt.Errorf("stuntgres: incomplete dependent grouping set")
			}
		}
	}
	project := definition.frames[frame]
	if project == nil || project.Syntax == nil || project.Syntax.GroupBy == nil {
		return fmt.Errorf("stuntgres: missing ordinary grouping dependency clause")
	}
	if !project.Syntax.GroupBy.Ordinary() {
		return fmt.Errorf("stuntgres: nonordinary grouping dependency clause")
	}
	used := make([]bool, len(n.Dependencies))
	for i, proof := range n.Dependencies {
		if err := validateGroupConstraint(view, proof); err != nil {
			return err
		}
		if proof.Frame != frame || frame == 0 || proof.Occurrence < 0 || proof.Occurrence >= len(definition.sources) || definition.sources[proof.Occurrence].Ref != proof.Parent {
			return fmt.Errorf("stuntgres: invalid grouping dependency occurrence")
		}
		for _, prior := range n.Dependencies[:i] {
			if prior.Occurrence == proof.Occurrence {
				return fmt.Errorf("stuntgres: duplicate grouping dependency occurrence")
			}
		}
		source, start := groupDependencySource(n.Child, proof.Occurrence, 0)
		if source == nil || source.Ref != proof.Parent {
			return fmt.Errorf("stuntgres: missing grouping dependency source")
		}
		attributes := definition.sources[proof.Occurrence].Attributes
		for _, attribute := range proof.Attributes {
			physical := slices.Index(attributes, attribute)
			found := false
			for _, key := range n.Keys {
				column, ok := key.(plan.ColumnExpr)
				found = found || ok && column.Frame == frame && column.Scope == 1 &&
					physical >= 0 && column.Slot == start+physical &&
					column.Origin.Domain() == proof.Parent.Domain() && column.Origin.Epoch == proof.Parent.Epoch &&
					column.Origin.Address.ClassID == proof.Parent.Address.ClassID &&
					column.Origin.Address.ObjectID == proof.Parent.Address.ObjectID && column.Origin.Address.SubID == attribute
			}
			if !found {
				return fmt.Errorf("stuntgres: incomplete grouping dependency key")
			}
		}
	}
	admits := func(bound expr.Expr) bool {
		for i, proof := range n.Dependencies {
			source, start := groupDependencySource(n.Child, proof.Occurrence, 0)
			attributes := definition.sources[proof.Occurrence].Attributes
			valid := false
			switch e := bound.(type) {
			case plan.ColumnExpr:
				physical := e.Slot - start
				valid = e.Frame == frame && e.Scope == 1 && physical >= 0 && physical < len(attributes) && e.Origin.Domain() == proof.Parent.Domain() && e.Origin.Epoch == proof.Parent.Epoch && e.Origin.Address.ClassID == proof.Parent.Address.ClassID && e.Origin.Address.ObjectID == proof.Parent.Address.ObjectID && e.Origin.Address.SubID == attributes[physical] && physical < len(source.Schema()) && source.Schema()[physical].Usage == e.ResultUsage()
			case plan.SystemAttributeExpr:
				valid = e.Frame == frame && e.Scope == 1 && e.QueryOccurrence == proof.Occurrence+1 && e.Owner == proof.Parent && catalog.SystemAttributeName(e.SubID) != ""
			case plan.RowExpr:
				valid = e.WholeNames != nil && e.PresenceFrame == frame && e.PresenceSlot != 0 && e.PresenceSlot == source.PresenceSlot && len(e.Fields) == len(attributes)
				for physical, field := range e.Fields {
					column, ok := field.(plan.ColumnExpr)
					valid = valid && ok && column.Frame == frame && column.Scope == 1 &&
						physical < len(attributes) && column.Slot == start+physical &&
						column.Origin.Address.ClassID == proof.Parent.Address.ClassID &&
						column.Origin.Address.ObjectID == proof.Parent.Address.ObjectID &&
						column.Origin.Address.SubID == attributes[physical] &&
						column.Origin.Domain() == proof.Parent.Domain() && column.Origin.Epoch == proof.Parent.Epoch &&
						physical < len(source.Schema()) && column.ResultUsage() == source.Schema()[physical].Usage
				}
			}
			if valid {
				used[i] = true
				return true
			}
		}
		return false
	}
	for i, dependent := range n.Dependent {
		if dependent == nil || dependent.ResultUsage() != n.Schema()[n.DependentStart()+i].Usage || !admits(dependent) {
			return fmt.Errorf("stuntgres: invalid dependent group expression")
		}
	}
	if slices.Contains(used, false) {
		return fmt.Errorf("stuntgres: unused grouping dependency proof")
	}
	return nil
}

func groupDependencyAddresses(root plan.Node, visit func(ids.ObjectAddress)) {
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if aggregate, ok := node.(*plan.AggregateNode); ok {
			for _, proof := range aggregate.Dependencies {
				visit(proof.Constraint)
			}
		}
	}, func(expr.Expr) {}, true)
}
