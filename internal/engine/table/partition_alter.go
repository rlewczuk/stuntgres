package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PartitionConstraintActions is deliberately a whole-list whitelist. Named DROP
// is admitted only for this family's facts (or a genuinely missing name), never
// as a back door to key, index or relationship propagation.
func PartitionConstraintActions(root *catalog.Relation, actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	names := map[string]bool{}
	for _, c := range root.Constraints.Check {
		names[c.Name] = true
	}
	for _, c := range root.Constraints.NotNull {
		names[c.Name] = true
	}
	for _, a := range actions {
		switch a.Action {
		case parser.SetAccessMethod:
			// Method intent belongs only to the selected root, never its leaves.
		case parser.SetRelationOptions, parser.ResetRelationOptions, parser.EnableRule, parser.DisableRule, parser.EnableReplicaRule, parser.EnableAlwaysRule, parser.EnableTrigger, parser.DisableTrigger, parser.EnableReplicaTrigger, parser.EnableAlwaysTrigger:
			// Root-only metadata companions do not expand the shape whitelist.
		case parser.AddCheck:
			if a.Check == nil {
				return false
			}
		case parser.AddNotNull:
		case parser.SetNotNull, parser.DropNotNull:
		case parser.ValidateConstraint, parser.AlterConstraintEnforcement:
			// Resolve kind and missing-name diagnostics at the actual misc phase.
		case parser.DropConstraint:
			if catalog.RelationConstraintNameExists(root, a.Constraint.Text) && !names[a.Constraint.Text] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// AlterGraph is statement-owned constraint scratch, not partition topology.
// Only ordinary relations persist a TableInheritance; partitions persist exact
// provenance on the constraint itself.
func AlterGraph(d *catalog.Relation) *catalog.TableInheritance {
	if !catalog.HierarchyRelation(d) {
		return catalog.CloneInheritance(d.Inheritance)
	}
	g := &catalog.TableInheritance{Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
	if d.Membership != nil {
		g.Parents = []catalog.InheritanceParent{{Relation: d.Membership.Parent, Seqno: 1}}
	}
	for _, c := range d.Constraints.Check {
		g.Checks[c.Name] = catalog.ConstraintOrigin(c.PartitionOrigin)
	}
	for _, c := range d.Constraints.NotNull {
		g.NotNull[c.Attribute] = catalog.ConstraintOrigin(c.PartitionOrigin)
	}
	return g
}

func planPartitionAlter(view catalog.Read, root *catalog.Relation, raw *parser.AlterTable) (InheritanceAlterPlan, bool, error) {
	if !PartitionConstraintActions(root, raw.Actions) {
		return InheritanceAlterPlan{}, false, sqlError("0A000", "altering partition hierarchies is unsupported")
	}
	if err := PartitionConstraintDirectGuard(root, raw.Actions); err != nil {
		return InheritanceAlterPlan{}, false, err
	}
	targets := InheritanceAlterOrder(view, root)
	if raw.Only && len(targets) > 1 {
		for _, a := range raw.Actions {
			fresh := false
			switch a.Action {
			case parser.AddCheck:
				fresh = !slices.ContainsFunc(root.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == a.Check.Name.Text })
			case parser.SetNotNull, parser.AddNotNull:
				fresh = true
				if slot, ok := root.Index[a.Column.Text]; ok {
					fresh = !slices.ContainsFunc(root.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == root.Attributes[slot] })
				}
			}
			if fresh {
				err := sqlError("42P16", "constraint must be added to child tables too").(*sqlstate.Error)
				if a.Action == parser.SetNotNull {
					err.Hint = "Do not specify the ONLY keyword."
				}
				return InheritanceAlterPlan{}, false, err
			}
		}
	}
	return InheritanceAlterPlan{Root: root, Targets: targets, Partition: true, Only: raw.Only, Actions: raw.Actions, Selected: map[ids.ObjectAddress]bool{}, Events: map[ids.ObjectAddress][]InheritanceAlterEvent{}}, InheritancePreparatoryLocks(root, raw.Actions, raw.Only), nil
}

func PartitionConstraintDirectGuard(root *catalog.Relation, actions []parser.AlterTableAction) error {
	for _, a := range actions {
		if a.Action == parser.DropConstraint {
			for _, c := range root.Constraints.Check {
				if c.Name == a.Constraint.Text && catalog.ConstraintOrigin(c.PartitionOrigin).Count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, root.Name()))
				}
			}
		}
		for _, c := range root.Constraints.NotNull {
			match := a.Action == parser.DropConstraint && c.Name == a.Constraint.Text
			if slot, ok := root.Index[a.Column.Text]; ok && a.Action == parser.DropNotNull {
				match = c.Attribute == root.Attributes[slot]
			}
			if match && catalog.ConstraintOrigin(c.PartitionOrigin).Count() != 0 {
				if a.Action == parser.DropNotNull {
					return sqlError("42P16", fmt.Sprintf(`column "%s" is marked NOT NULL in parent table`, a.Column.Text))
				}
				return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, root.Name()))
			}
		}
	}
	return nil
}

func partitionInheritedActions(state *InheritanceAlterPlan, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	graph := AlterGraph(old)
	var actions []parser.AlterTableAction
	for _, a := range InheritanceParentActions(state, old) {
		switch a.Action {
		case parser.ValidateConstraint:
			// Keep the logical column until the completed child definition exists.
			actions = append(actions, a)
		case parser.AddCheck, parser.AddNotNull, parser.SetNotNull:
			if !state.Only {
				actions = append(actions, a)
			}
		case parser.DropConstraint:
			p := graph.Checks[a.Constraint.Text]
			p.Origins = nil
			if state.Only {
				p.Local = true
			}
			graph.Checks[a.Constraint.Text] = p
			if !p.Local {
				actions = append(actions, a)
			}
		case parser.DropNotNull:
			if slot, ok := old.Index[a.Column.Text]; ok {
				attr := old.Attributes[slot]
				p := graph.NotNull[attr]
				p.Origins = nil
				if state.Only {
					p.Local = true
				}
				graph.NotNull[attr] = p
				if !p.Local {
					actions = append(actions, a)
				}
			}
		}
	}
	return actions, graph, nil
}

func finishPartitionCandidate(state *InheritanceAlterPlan, view catalog.Read, old, next *catalog.Relation) (*catalog.Relation, error) {
	copy := *next
	copy.Constraints.Check = slices.Clone(next.Constraints.Check)
	copy.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
	graph := state.Graph
	var incoming []parser.AlterTableAction
	if state.Descendant {
		incoming = InheritanceParentActions(state, old)
	}
	for i := range copy.Constraints.Check {
		c := &copy.Constraints.Check[i]
		p := catalog.ConstraintOrigin(c.PartitionOrigin)
		if state.Descendant {
			p = graph.Checks[c.Name]
		}
		if state.Descendant {
			for _, a := range incoming {
				if a.Action != parser.AddCheck || a.Check.Name.Text != c.Name {
					continue
				}
				parent := view.MustRelation(old.Membership.Parent)
				for _, pc := range parent.Constraints.Check {
					if pc.Name == c.Name {
						p = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Constraint: pc.OID}}}
					}
				}
			}
		}
		c.PartitionOrigin = &p
	}
	for i := range copy.Constraints.NotNull {
		c := &copy.Constraints.NotNull[i]
		p := catalog.ConstraintOrigin(c.PartitionOrigin)
		if state.Descendant {
			p = graph.NotNull[c.Attribute]
		}
		if state.Descendant {
			for _, a := range incoming {
				if a.Action != parser.AddNotNull && a.Action != parser.SetNotNull {
					continue
				}
				slot, ok := copy.Index[a.Column.Text]
				if !ok || copy.Attributes[slot] != c.Attribute {
					continue
				}
				parent := view.MustRelation(old.Membership.Parent)
				ps := parent.Index[a.Column.Text]
				for _, pc := range parent.Constraints.NotNull {
					if pc.Attribute == parent.Attributes[ps] {
						p.Origins = []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: pc.Attribute, Constraint: pc.OID}}
					}
				}
			}
		}
		c.PartitionOrigin = &p
	}
	return &copy, nil
}
