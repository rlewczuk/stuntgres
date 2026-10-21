package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

// InheritedAlterActions are the actions plan propagates to the inheritance
// member old, with old's updated inheritance graph. view reads the members as
// staged so far; retired reads the retired constraint history.
func InheritedAlterActions(plan *InheritanceAlterPlan, view, retired catalog.Read, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	state := plan
	graph := catalog.CloneInheritance(old.Inheritance)
	var actions []parser.AlterTableAction
	droppedColumns := make(map[string]bool)
	for _, a := range InheritanceParentActions(state, old) {
		switch a.Action {
		case parser.AddColumn:
			if state.Only {
				continue
			}
			name := a.Definition.Name
			_, existing := old.Index[name]
			existing = existing && !droppedColumns[name]
			if existing {
				slot := old.Index[name]
				for _, parentAddress := range graph.Parents {
					parent := state.DefinitionRelation(view, parentAddress)
					if pslot, ok := parent.Index[name]; ok {
						if err := analyze.InheritanceColumnConflict(view, old.Columns[slot], parent.Columns[pslot], true); err != nil {
							return nil, nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, old.Name(), name))
						}
					}
				}
			}
			definition := *a.Definition
			definition.Constraints = nil
			for _, constraint := range a.Definition.Constraints {
				if constraint.NotNull || constraint.Check != nil || constraint.Unique != nil || constraint.ForeignKey != nil {
					continue
				}
				definition.Constraints = append(definition.Constraints, constraint)
			}
			if !existing {
				a.Definition = &definition
				actions = append(actions, a)
			}
		case parser.DropColumn:
			slot, exists := old.Index[a.Column.Text]
			if !exists {
				continue
			}
			attribute := old.Attributes[slot]
			provenance := graph.Columns[attribute]
			var origins []catalog.InheritanceOrigin
			removed := false
			for _, origin := range provenance.Origins {
				if InheritanceRemovedOrigin(state, origin, parser.DropColumn) {
					removed = true
				} else {
					origins = append(origins, origin)
				}
			}
			provenance.Origins = origins
			if state.Only && removed {
				provenance.Local = true
			}
			graph.Columns[attribute] = provenance
			if provenance.Local || len(origins) != 0 {
				InheritRetiredColumnConstraints(state, retired, old, graph, attribute)
			}
			if !provenance.Local && len(origins) == 0 {
				actions = append(actions, a)
				droppedColumns[a.Column.Text] = true
			}
		case parser.AlterColumnType:
			if state.Only {
				continue
			}
			slot, exists := old.Index[a.Column.Text]
			if !exists {
				continue
			}
			for _, origin := range graph.Columns[old.Attributes[slot]].Origins {
				if !state.Selected[origin.Parent] {
					return nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s" of relation "%s"`, a.Column.Text, old.Name()))
				}
			}
			actions = append(actions, a)
		case parser.SetExpression, parser.DropExpression:
			if !state.Only {
				actions = append(actions, a)
			}
		case parser.SetNotNull, parser.AddNotNull:
			if state.Only || a.NoInherit {
				continue
			}
			actions = append(actions, a)
		case parser.DropNotNull, parser.DropConstraint:
			matched := false
			for _, c := range old.Constraints.NotNull {
				match := a.Action == parser.DropConstraint && c.Name == a.Constraint.Text
				if a.Action == parser.DropNotNull {
					if slot, ok := old.Index[a.Column.Text]; ok {
						match = c.Attribute == old.Attributes[slot]
					}
				}
				if !match {
					continue
				}
				matched = true
				provenance := graph.NotNull[c.Attribute]
				provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
					return InheritanceRemovedOrigin(state, origin, parser.DropNotNull)
				})
				if state.Only && provenance.Count() == 0 {
					provenance.Local = true
				}
				graph.NotNull[c.Attribute] = provenance
				if !provenance.Local && provenance.Count() == 0 {
					actions = append(actions, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: c.Name}, Mode: a.Mode})
				}
			}
			if matched || a.Action == parser.DropNotNull {
				continue
			}
			for _, c := range old.Constraints.Check {
				if c.Name == a.Constraint.Text {
					provenance := graph.Checks[c.Name]
					provenance.Origins = slices.DeleteFunc(slices.Clone(provenance.Origins), func(origin catalog.InheritanceOrigin) bool {
						return InheritanceRemovedOrigin(state, origin, parser.DropConstraint)
					})
					if state.Only && provenance.Count() == 0 {
						provenance.Local = true
					}
					graph.Checks[c.Name] = provenance
					if !provenance.Local && provenance.Count() == 0 {
						actions = append(actions, a)
					}
				}
			}
		case parser.AddCheck:
			if state.Only || a.Check.NoInherit {
				continue
			}
			copy := *a.Check
			a.Check = &copy
			actions = append(actions, a)
		case parser.AddUnique:
			if !state.Only && a.Unique.Primary {
				for _, column := range a.Unique.Columns {
					if c := InheritedNotNull(view, graph.Parents, column.Text); c != nil {
						actions = append(actions, parser.AlterTableAction{Action: parser.AddNotNull, Column: column, Constraint: parser.Identifier{Text: c.Name}, NotValid: c.NotValid})
					}
				}
			}
		}
	}
	return actions, graph, nil
}

// FinishInheritanceAlterCandidate records next's inheritance provenance after
// the actions of plan; next is returned unchanged when plan is nil or old
// takes no part in inheritance. view reads the members as staged.
func FinishInheritanceAlterCandidate(plan *InheritanceAlterPlan, view catalog.Read, old, next *catalog.Relation) (*catalog.Relation, error) {
	state := plan
	if state == nil || old.Inheritance == nil {
		return next, nil
	}
	graph := state.Graph
	if !state.Descendant && next.Inheritance != nil {
		graph = catalog.CloneInheritance(next.Inheritance)
	}
	for i, attribute := range next.Attributes {
		provenance, exists := graph.Columns[attribute]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, a := range state.Actions {
				if a.Action == parser.AddColumn && a.Definition.Name == next.Columns[i].Name {
					for _, constraint := range a.Definition.Constraints {
						provenance.ExplicitDefault = provenance.ExplicitDefault || constraint.Default != nil
						provenance.ExplicitGeneration = provenance.ExplicitGeneration || constraint.Generation != nil
					}
				}
				if a.Action == parser.SetExpression && a.Column.Text == next.Columns[i].Name {
					provenance.ExplicitGeneration = true
				}
			}
		}
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			for _, parentAddress := range graph.Parents {
				parent := view.MustRelation(parentAddress)
				if slot, ok := parent.Index[next.Columns[i].Name]; ok {
					origins = append(origins, catalog.InheritanceOrigin{Parent: parentAddress, Attribute: parent.Attributes[slot]})
				}
			}
			provenance.Origins = origins
		}
		if state.Descendant {
			var err error
			provenance, err = RetainInheritedRecipes(view, next, attribute, provenance)
			if err != nil {
				return nil, err
			}
		}
		graph.Columns[attribute] = provenance
	}
	for attribute := range graph.Columns {
		if !next.HasAttribute(attribute) {
			delete(graph.Columns, attribute)
			delete(graph.NotNull, attribute)
		}
	}
	for _, c := range next.Constraints.Check {
		provenance, exists := graph.Checks[c.Name]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, action := range state.Actions {
				if action.Action == parser.AddCheck && action.Check.Name.Text == c.Name {
					provenance.Local = true
				}
			}
		}
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				for _, pc := range parent.Constraints.Check {
					if pc.Name == c.Name && !pc.NoInherit {
						origins = append(origins, catalog.InheritanceOrigin{Parent: address, Constraint: pc.OID})
					}
				}
			}
			provenance.Origins = origins
		}
		graph.Checks[c.Name] = provenance
	}
	for name := range graph.Checks {
		if !slices.ContainsFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name }) {
			delete(graph.Checks, name)
		}
	}
	for _, c := range next.Constraints.NotNull {
		provenance, exists := graph.NotNull[c.Attribute]
		if !exists {
			provenance.Local = !state.Descendant
		}
		if !state.Descendant {
			for _, action := range state.Actions {
				if (action.Action == parser.SetNotNull || action.Action == parser.AddNotNull) && action.Column.Text == next.Columns[next.IndexForAttribute(c.Attribute)].Name {
					provenance.Local = true
				}
			}
		}
		if state.Descendant {
			var origins []catalog.InheritanceOrigin
			name := next.Columns[next.IndexForAttribute(c.Attribute)].Name
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				if slot, ok := parent.Index[name]; ok {
					for _, pc := range parent.Constraints.NotNull {
						if pc.Attribute == parent.Attributes[slot] && !pc.NoInherit {
							origins = append(origins, catalog.InheritanceOrigin{Parent: address, Attribute: pc.Attribute, Constraint: pc.OID})
						}
					}
				}
			}
			provenance.Origins = origins
		}
		graph.NotNull[c.Attribute] = provenance
	}
	for attribute := range graph.NotNull {
		if !slices.ContainsFunc(next.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute }) {
			delete(graph.NotNull, attribute)
		}
	}
	copy := *next
	copy.Inheritance = graph
	if copy.Revision() == old.Revision() {
		copy.ObjectRevision++
	}
	return &copy, nil
}
