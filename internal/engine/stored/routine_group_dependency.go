package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// routineGroupingAggregate follows only this query's unary stages. FROM,
// derived queries and set operands own separate frozen frame entries.
func routineGroupingAggregate(project *plan.ProjectNode) *plan.AggregateNode {
	if project == nil {
		return nil
	}
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.AggregateNode:
			return n
		case *plan.ProjectNode:
			if n.Frame != project.Frame {
				return nil
			}
			node = n.Child
		case *plan.FilterNode, *plan.SortNode, *plan.WindowNode,
			*plan.DistinctNode, *plan.DistinctOnNode, *plan.RowLockNode,
			*plan.SetProjectNode, *plan.EvaluationNode, *plan.LimitNode:
			node, _ = plan.QueryInputs(node)
		default:
			return nil
		}
	}
	return nil
}

func routineGroupingParent(dependencies []catalog.ExpressionDependency, parent ids.ObjectRef) bool {
	for _, dependency := range dependencies {
		ref := dependency.Ref
		ref.Address.SubID = 0
		if ref == parent && dependency.Revision != 0 && dependency.Purpose.Valid() {
			return true
		}
	}
	return false
}

func authenticateRoutineGrouping(view catalog.Read, dependencies []catalog.ExpressionDependency, proof plan.GroupDependency) error {
	if view.State() == nil {
		if proof.Parent.Domain() == nil || proof.Parent.Epoch == 0 || !routineGroupingParent(dependencies, proof.Parent) {
			return fmt.Errorf("stuntgres: missing original routine grouping parent capture")
		}
		return nil
	}
	if view.Owner() == nil || proof.Parent.Domain() != view.Owner().Domain || proof.Parent.Epoch != view.Epoch() || !routineGroupingParent(dependencies, proof.Parent) {
		return fmt.Errorf("stuntgres: missing or foreign routine grouping parent capture")
	}
	return validateGroupConstraint(view, proof)
}

// VisitGroupingConstraintDependencies projects attached proofs, not descriptor
// manifests. Normalized captures are the allocation-free parent dedup index;
// one genuine parent has at most one canonical nondeferrable primary key.
func (p *RoutineProgram) VisitGroupingConstraintDependencies(view catalog.Read, yield func(ids.ObjectAddress) bool) bool {
	for i, dependency := range p.dependencies {
		parent := dependency.Ref
		if parent.Address.ClassID != ids.RelationClassID {
			continue
		}
		parent.Address.SubID = 0
		duplicate := false
		for _, earlier := range p.dependencies[:i] {
			ref := earlier.Ref
			ref.Address.SubID = 0
			if ref == parent {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		var address ids.ObjectAddress
		inspect := func(presentation *View) bool {
			if presentation == nil {
				return false
			}
			for frame, project := range presentation.frames {
				aggregate := routineGroupingAggregate(project)
				if aggregate == nil {
					continue
				}
				for _, proof := range aggregate.Dependencies {
					// Compare logical parent first so forged authority cannot hide
					// behind a different domain or epoch at the same address.
					if proof.Parent.Address != parent.Address {
						continue
					}
					if view.Owner() == nil || view.State() == nil || proof.Parent != parent || proof.Frame != frame || authenticateRoutineGrouping(view, p.dependencies, proof) != nil {
						return false
					}
					address = proof.Constraint
				}
			}
			return true
		}
		for _, action := range p.actions {
			if !inspect(action.presentation) {
				return false
			}
			for _, presentation := range action.modifierPresentations {
				if !inspect(presentation) {
					return false
				}
			}
		}
		if address.ObjectID != 0 && !yield(address) {
			return false
		}
	}
	return true
}

// The publication/action boundary authenticates all retained roots, including
// proofs missing from the parent-driven projection's index or frame manifest.
// This focused check never re-admits retained callee metadata.
func validateRoutineGrouping(action RuleAction, dependencies []catalog.ExpressionDependency, view catalog.Read) error {
	check := func(presentation *View, root plan.Node, inventory bool) error {
		if presentation == nil || presentation.query == nil {
			return fmt.Errorf("stuntgres: missing routine grouping presentation")
		}
		var failure error
		var retained map[uint32]bool
		plan.VisitCTEQuery(root, func(node plan.Node) {
			if failure != nil {
				return
			}
			aggregate, ok := node.(*plan.AggregateNode)
			if !ok {
				return
			}
			if aggregate.DependentStart()+len(aggregate.Dependent) != len(aggregate.Schema()) ||
				(len(aggregate.Dependent) == 0) != (len(aggregate.Dependencies) == 0) {
				failure = fmt.Errorf("stuntgres: invalid routine dependent group layout")
				return
			}
			var frame uint32
			for id, project := range presentation.frames {
				if routineGroupingAggregate(project) == aggregate {
					frame = id
					break
				}
			}
			// Binding and presentation remap independently, so association is
			// structural when copy-on-change produced distinct stage owners.
			if frame == 0 && len(aggregate.Dependencies) != 0 {
				frame = aggregate.Dependencies[0].Frame
			}
			if frame == 0 && len(aggregate.Dependencies) == 0 && len(aggregate.Dependent) == 0 {
				return
			}
			project := presentation.frames[frame]
			frozen := routineGroupingAggregate(project)
			if frame == 0 || project == nil || project.Frame != frame || !sameRoutineGrouping(aggregate, frozen) {
				failure = fmt.Errorf("stuntgres: missing routine grouping frame association")
				return
			}
			if inventory && (len(aggregate.Dependent) != 0 || len(aggregate.Dependencies) != 0) {
				if retained == nil {
					retained = make(map[uint32]bool)
				}
				retained[frame] = true
			}
			failure = validateRoutineGroupingAggregate(presentation, aggregate, frame, dependencies, view)
			if failure == nil && frozen != aggregate {
				// Frozen scalar frames are independent retained owners, not an
				// index whose contents can be trusted after checking the root.
				failure = validateRoutineGroupingAggregate(presentation, frozen, frame, dependencies, view)
			}
		}, func(expr.Expr) {}, true)
		if failure == nil && inventory {
			for frame, project := range presentation.frames {
				aggregate := routineGroupingAggregate(project)
				if aggregate != nil && (len(aggregate.Dependent) != 0 || len(aggregate.Dependencies) != 0) && !retained[frame] {
					return fmt.Errorf("stuntgres: orphan routine grouping frame")
				}
			}
		}
		return failure
	}
	if action.presentation == nil || action.presentation.query == nil {
		return fmt.Errorf("stuntgres: missing routine grouping presentation")
	}
	if err := check(action.presentation, action.presentation.query.Root, true); err != nil {
		return err
	}
	for _, presentation := range action.modifierPresentations {
		if presentation == nil || presentation.query == nil {
			return fmt.Errorf("stuntgres: missing routine modifier grouping presentation")
		}
		if err := check(presentation, presentation.query.Root, true); err != nil {
			return err
		}
	}
	checkBinding := func(binding analyze.Binding, presentation *View) error {
		if binding.Query != nil {
			if err := check(presentation, binding.Query.Root, false); err != nil {
				return err
			}
		}
		if binding.Delete.Using != nil {
			if err := check(presentation, binding.Delete.Using, false); err != nil {
				return err
			}
		}
		if binding.Update != nil && binding.Update.From != nil {
			if err := check(presentation, binding.Update.From, false); err != nil {
				return err
			}
		}
		if binding.Merge != nil && binding.Merge.Source != nil {
			if err := check(presentation, binding.Merge.Source, false); err != nil {
				return err
			}
		}
		var failure error
		VisitRuleActionExpressions(binding, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(node expr.Expr) {
				if subquery, ok := node.(plan.SubqueryExpr); ok && subquery.QueryPlan != nil && failure == nil {
					failure = check(presentation, subquery.QueryPlan.Root, false)
				}
			})
		})
		return failure
	}
	if err := checkBinding(action.binding, action.presentation); err != nil {
		return err
	}
	return VisitRuleModifiers(action.binding, func(definition *plan.CTEDefinition, binding *analyze.Binding) error {
		return checkBinding(*binding, action.modifierPresentations[definition.Statement])
	})
}

func validateRoutineGroupingAggregate(presentation *View, aggregate *plan.AggregateNode, frame uint32, dependencies []catalog.ExpressionDependency, view catalog.Read) error {
	for _, proof := range aggregate.Dependencies {
		if proof.Frame != frame || proof.Occurrence < 0 || proof.Occurrence >= len(presentation.sources) || presentation.sources[proof.Occurrence].Ref != proof.Parent {
			return fmt.Errorf("stuntgres: invalid original routine grouping association")
		}
		source, _ := groupDependencySource(aggregate.Child, proof.Occurrence, 0)
		if source == nil || source.Ref != proof.Parent {
			return fmt.Errorf("stuntgres: missing original routine grouping source")
		}
		if err := authenticateRoutineGrouping(view, dependencies, proof); err != nil {
			return err
		}
	}
	if view.State() != nil {
		return validateGroupDependencyNode(view, presentation, aggregate, frame)
	}
	return nil
}

func sameRoutineGrouping(a, b *plan.AggregateNode) bool {
	if a == b {
		return a != nil
	}
	if b == nil || !slices.Equal(a.QuerySchema, b.QuerySchema) ||
		len(a.Keys) != len(b.Keys) || len(a.Grouping) != len(b.Grouping) ||
		len(a.Calls) != len(b.Calls) || len(a.Sets) != len(b.Sets) ||
		a.GroupingActive != b.GroupingActive || a.PresenceOnly != b.PresenceOnly ||
		len(a.Dependent) != len(b.Dependent) || len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	for i, key := range a.Keys {
		if !(*analyze.Binder)(nil).SameGroupExpression(key, b.Keys[i]) {
			return false
		}
	}
	for i, set := range a.Sets {
		if !slices.Equal(set, b.Sets[i]) {
			return false
		}
	}
	for i, proof := range a.Dependencies {
		other := b.Dependencies[i]
		if proof.Parent != other.Parent || proof.Constraint != other.Constraint || proof.Frame != other.Frame || proof.Occurrence != other.Occurrence || !slices.Equal(proof.Attributes, other.Attributes) {
			return false
		}
	}
	for i, dependent := range a.Dependent {
		if !(*analyze.Binder)(nil).SameGroupExpression(dependent, b.Dependent[i]) {
			return false
		}
	}
	return true
}
