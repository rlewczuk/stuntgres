package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// CheckAlterConstraint resolves every explicitly selected property before a
// no-op can elide work. The ONLY intent belongs to the original command, not
// the per-node ONLY commands used by inheritance staging.
func CheckAlterConstraint(old *catalog.Relation, a parser.AlterTableAction, only bool) error {
	if only && old.RelationKind == catalog.PartitionedRelation {
		err := sqlError("42P16", "constraint must be altered in child tables too").(*sqlstate.Error)
		err.Hint = "Do not specify the ONLY keyword."
		return err
	}
	name := a.Constraint.Text
	if !catalog.RelationConstraintNameExists(old, name) {
		return sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	fk := slices.ContainsFunc(old.Constraints.ForeignKeys, func(c catalog.ForeignKeyConstraint) bool { return c.Name == name })
	if (a.DeferrabilitySet || a.InitiallySet) && !fk {
		return sqlError("42809", fmt.Sprintf(`constraint "%s" of relation "%s" is not a foreign key constraint`, name, old.Name()))
	}
	if a.EnforcementSet && !fk {
		return sqlError("42809", fmt.Sprintf(`cannot alter enforceability of constraint "%s" of relation "%s"`, name, old.Name()))
	}
	if a.InheritabilitySet && !slices.ContainsFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Name == name }) {
		return sqlError("42809", fmt.Sprintf(`constraint "%s" of relation "%s" is not a not-null constraint`, name, old.Name()))
	}
	return nil
}

// AlterConstraintInheritCandidate changes only the selected NN slice, never
// its identity, validity, column nullability, primary key or physical backing.
func AlterConstraintInheritCandidate(old *catalog.Relation, name string, noinherit bool) (*catalog.Relation, error) {
	at := slices.IndexFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Name == name })
	if at < 0 {
		if catalog.RelationConstraintNameExists(old, name) {
			return old, sqlError("42809", fmt.Sprintf(`constraint "%s" of relation "%s" is not a not-null constraint`, name, old.Name()))
		}
		return old, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	fact := old.Constraints.NotNull[at]
	if noinherit {
		if old.RelationKind == catalog.PartitionedRelation {
			return old, sqlError("0A000", fmt.Sprintf(`not-null constraint "%s" on partitioned table "%s" cannot be NO INHERIT`, name, old.Name()))
		}
		count := catalog.ConstraintOrigin(fact.PartitionOrigin).Count()
		if old.Inheritance != nil {
			count += old.Inheritance.NotNull[fact.Attribute].Count()
		}
		if count != 0 {
			return old, sqlError("55000", fmt.Sprintf(`cannot alter inherited constraint "%s" on relation "%s"`, name, old.Name()))
		}
	}
	if fact.NoInherit == noinherit {
		return old, nil
	}
	next := *old
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	next.Constraints.NotNull[at].NoInherit = noinherit
	return &next, nil
}

// ConstraintModeNoOp admits only a single bare or already-matching NN mode,
// after normal command authorization/preparation. It allocates no successor or
// inheritance journal and never skips direction-specific eligibility guards.
func ConstraintModeNoOp(old *catalog.Relation, actions []parser.AlterTableAction, only bool) (bool, error) {
	if len(actions) != 1 {
		return false, nil
	}
	a := actions[0]
	if !parser.IsAlterConstraint(a.Action) || a.EnforcementSet || a.DeferrabilitySet || a.InitiallySet {
		return false, nil
	}
	if err := CheckAlterConstraint(old, a, only); err != nil {
		return false, err
	}
	if !a.InheritabilitySet {
		return true, nil
	}
	for _, fact := range old.Constraints.NotNull {
		if fact.Name == a.Constraint.Text && fact.NoInherit == a.NoInherit {
			_, err := AlterConstraintInheritCandidate(old, fact.Name, a.NoInherit)
			return err == nil, err
		}
	}
	return false, nil
}

func ConstraintModesOnly(actions []parser.AlterTableAction) bool {
	for _, a := range actions {
		if !parser.IsAlterConstraint(a.Action) {
			return false
		}
	}
	return len(actions) != 0
}

// Mode events carry the exact source parent/attribute/constraint through the
// existing per-parent queue. Detach localizes only direct children; attach
// constructs missing facts through SET NOT NULL, whose record step alone can
// relay the event farther. Existing compatible facts stop that branch.
func inheritedConstraintModes(state *InheritanceAlterPlan, old *catalog.Relation, graph *catalog.TableInheritance) []parser.AlterTableAction {
	var actions []parser.AlterTableAction
	for _, edge := range graph.Parents {
		for _, event := range state.Events[edge.Relation] {
			if event.Action.Action != parser.AlterConstraintInherit {
				continue
			}
			name := event.Action.Column.Text
			slot, ok := old.Index[name]
			if !ok {
				continue
			}
			attribute := old.Attributes[slot]
			p := graph.NotNull[attribute]
			origin := catalog.InheritanceOrigin{Parent: edge.Relation, Attribute: event.Attribute, Constraint: event.Constraint}
			if event.Action.NoInherit {
				p.Origins = slices.DeleteFunc(slices.Clone(p.Origins), func(o catalog.InheritanceOrigin) bool { return o == origin })
				p.Local = true
			} else {
				if !slices.Contains(p.Origins, origin) {
					p.Origins = append(slices.Clone(p.Origins), origin)
				}
				if !slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return a.Column.Text == name }) {
					actions = append(actions, parser.AlterTableAction{Action: parser.SetNotNull, Column: parser.Identifier{Text: name}, Constraint: event.Action.Constraint, InheritabilitySet: true})
				}
			}
			graph.NotNull[attribute] = p
		}
	}
	return actions
}

func modeParentColumn(state *InheritanceAlterPlan, graph *catalog.TableInheritance, name string) bool {
	for _, edge := range graph.Parents {
		for _, event := range state.Events[edge.Relation] {
			if event.Action.Action == parser.AlterConstraintInherit && event.Action.Column.Text == name {
				return true
			}
		}
	}
	return false
}

func recordConstraintMode(events []InheritanceAlterEvent, relation *catalog.Relation, fact catalog.NotNullConstraint, noinherit bool) []InheritanceAlterEvent {
	a := parser.AlterTableAction{Action: parser.AlterConstraintInherit, Column: parser.Identifier{Text: relation.Columns[relation.IndexForAttribute(fact.Attribute)].Name}, Constraint: parser.Identifier{Text: fact.Name}, InheritabilitySet: true, NoInherit: noinherit}
	return append(events, InheritanceAlterEvent{Action: a, Attribute: fact.Attribute, Constraint: ids.OID(fact.OID)})
}

// RecordAssignedConstraintModes completes newly constructed NN event identities
// at the ordinary OID-assignment frontier, before any child consumes the queue.
func (state *InheritanceAlterPlan) RecordAssignedConstraintModes(after *catalog.Relation) {
	if state == nil {
		return
	}
	events := state.Events[after.Address()]
	for i := range events {
		event := &events[i]
		if event.Action.Action != parser.AlterConstraintInherit || event.Constraint != 0 {
			continue
		}
		for _, fact := range after.Constraints.NotNull {
			if fact.Attribute == event.Attribute && fact.Name == event.Action.Constraint.Text {
				event.Constraint = fact.OID
				break
			}
		}
	}
}
