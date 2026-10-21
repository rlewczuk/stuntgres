package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// inheritanceAlterPlan is the statement's recursion plan: the ordered targets,
// the actions and the events each node recorded for its children. It holds no
// stage or journal; inheritanceAlterState carries those.
type InheritanceAlterPlan struct {
	Root       *catalog.Relation
	Targets    []*catalog.Relation
	Selected   map[ids.ObjectAddress]bool
	Graph      *catalog.TableInheritance
	Descendant bool
	Only       bool
	Actions    []parser.AlterTableAction
	Events     map[ids.ObjectAddress][]InheritanceAlterEvent
	Validating map[string]bool
	Preparing  bool
	Definition InheritanceDefinitionReader
	Tablespace ids.ObjectRef          // Prepared root-only destination; never propagated.
	Types      []AlterTypePreparation // Original root TYPE occurrences, consumed on root re-entry.
}

// Unlike the OF-type entry, ordinary propagation includes its named root.
func InheritanceAlterOrder(view catalog.Read, root *catalog.Relation) []*catalog.Relation {
	selected := map[ids.ObjectAddress]*catalog.Relation{}
	var collect func(*catalog.Relation)
	collect = func(d *catalog.Relation) {
		if selected[d.Address()] != nil {
			return
		}
		selected[d.Address()] = d
		for _, child := range catalog.InheritanceChildren(view, d.Address()) {
			collect(child)
		}
	}
	collect(root)
	var result []*catalog.Relation
	seen := map[ids.ObjectAddress]bool{}
	var visit func(*catalog.Relation)
	visit = func(d *catalog.Relation) {
		if seen[d.Address()] {
			return
		}
		seen[d.Address()] = true
		if d.Inheritance != nil {
			for _, parent := range d.Inheritance.Parents {
				if p := selected[parent]; p != nil {
					visit(p)
				}
			}
		}
		result = append(result, d)
	}
	addresses := make([]ids.ObjectAddress, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	for _, address := range addresses {
		visit(selected[address])
	}
	return result
}

func InheritanceRecursiveAction(root *catalog.Relation, a parser.AlterTableAction, only bool) bool {
	switch a.Action {
	case parser.SetTablespace:
		return false // Assignment is always root-only, independent of ONLY/star.
	case parser.AddColumn, parser.DropColumn, parser.AlterColumnType:
		return true
	case parser.SetExpression, parser.DropExpression:
		return !only
	case parser.SetNotNull, parser.AddNotNull:
		if a.NoInherit {
			return false
		}
		if slot, ok := root.Index[a.Column.Text]; ok {
			for _, c := range root.Constraints.NotNull {
				if c.Attribute == root.Attributes[slot] && (!c.NotValid || a.Action == parser.AddNotNull) {
					return false
				}
			}
		}
		return true
	case parser.DropNotNull:
		if slot, ok := root.Index[a.Column.Text]; ok {
			for _, c := range root.Constraints.NotNull {
				if c.Attribute == root.Attributes[slot] {
					return !c.NoInherit
				}
			}
		}
	case parser.DropConstraint:
		for _, c := range root.Constraints.NotNull {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
		for _, c := range root.Constraints.Check {
			if c.Name == a.Constraint.Text {
				return !c.NoInherit
			}
		}
	case parser.AddCheck:
		for _, c := range root.Constraints.Check {
			if a.Check.Name.Text != "" && c.Name == a.Check.Name.Text {
				return false
			}
		}
		return !a.Check.NoInherit
	case parser.AddUsingIndex:
		return a.UsingIndex.Primary
	case parser.AddUnique:
		if a.Unique.Primary {
			for _, column := range a.Unique.Columns {
				if slot, ok := root.Index[column.Text]; !ok || root.Columns[slot].Nullable {
					return true
				}
			}
		}
	}
	return false
}

func InheritanceOnlyActions(root *catalog.Relation, children bool, actions []parser.AlterTableAction) ([]parser.AlterTableAction, error) {
	if !children {
		return actions, nil
	}
	next := slices.Clone(actions)
	for i, a := range next {
		switch a.Action {
		case parser.AddColumn:
			return nil, sqlError("42P16", "column must be added to child tables too")
		case parser.AlterColumnType:
			return nil, sqlError("42P16", fmt.Sprintf(`type of inherited column "%s" must be changed in child tables too`, a.Column.Text))
		case parser.AddCheck:
			existing := slices.ContainsFunc(root.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == a.Check.Name.Text })
			if !a.Check.NoInherit && !existing {
				return nil, sqlError("42P16", "constraint must be added to child tables too")
			}
		case parser.AddNotNull:
			existing := false
			if slot, ok := root.Index[a.Column.Text]; ok {
				existing = slices.ContainsFunc(root.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == root.Attributes[slot] })
			}
			if !a.NoInherit && !existing {
				return nil, sqlError("42P16", "constraint must be added to child tables too")
			}
		case parser.SetNotNull:
			// Retain ONLY intent for an existing invalid fact too; validation cannot
			// silently stop above children. Existing valid facts remain genuine no-ops.
			next[i].NoInherit = true
		case parser.DropExpression:
			return nil, sqlError("0A000", "ALTER TABLE / DROP EXPRESSION must be applied to child tables too")
		}
	}
	return next, nil
}

func InheritedNotNull(view catalog.Read, parents []ids.ObjectAddress, name string) *catalog.NotNullConstraint {
	for _, address := range parents {
		parent := view.MustRelation(address)
		if slot, exists := parent.Index[name]; exists {
			for i := range parent.Constraints.NotNull {
				c := &parent.Constraints.NotNull[i]
				if c.Attribute == parent.Attributes[slot] && !c.NoInherit {
					return c
				}
			}
		}
	}
	return nil
}

// PlanInheritanceAlter checks ALTER TABLE raw on the inheritance root and
// plans its recursion: the actions (ONLY rewritten), the targets in recursion
// order, and whether preparatory locks are needed before the targets are
// visited.
func PlanInheritanceAlter(view catalog.Read, root *catalog.Relation, raw *parser.AlterTable) (InheritanceAlterPlan, bool, error) {
	actions := raw.Actions
	if err := InheritanceAlterActionsGuard(view, root, actions); err != nil {
		return InheritanceAlterPlan{}, false, err
	}
	children := catalog.InheritanceChildren(view, root.Address())
	if raw.Only {
		for _, a := range raw.Actions {
			if a.Action == parser.AddUnique && a.Unique.Primary && len(children) != 0 {
				for _, column := range a.Unique.Columns {
					if slot, ok := root.Index[column.Text]; ok && root.Columns[slot].Nullable {
						return InheritanceAlterPlan{}, false, sqlError("XX000", fmt.Sprintf(`column "%s" of table "%s" is not marked NOT NULL`, column.Text, children[0].Name()))
					}
				}
			}
		}
		var err error
		actions, err = InheritanceOnlyActions(root, len(children) != 0, actions)
		if err != nil {
			return InheritanceAlterPlan{}, false, err
		}
	}
	preparatory := InheritancePreparatoryLocks(actions, raw.Only)
	recurse := preparatory || slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool {
		return InheritanceRecursiveAction(root, a, raw.Only) || a.Action == parser.DropColumn || a.Action == parser.DropNotNull || a.Action == parser.DropConstraint
	})
	targets := []*catalog.Relation{root}
	if recurse {
		targets = InheritanceAlterOrder(view, root)
	}
	return InheritanceAlterPlan{Root: root, Targets: targets, Selected: map[ids.ObjectAddress]bool{}, Only: raw.Only, Actions: actions, Events: make(map[ids.ObjectAddress][]InheritanceAlterEvent)}, preparatory, nil
}
