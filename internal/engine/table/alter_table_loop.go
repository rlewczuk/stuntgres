package table

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// OnlySetNotNull rewrites the SET NOT NULL actions of ALTER TABLE ONLY s on old
// as NO INHERIT when old has children or already holds a NO INHERIT NOT NULL
// on the column. It returns s itself when s has no SET NOT NULL.
func OnlySetNotNull(view catalog.Read, old *catalog.Relation, s *parser.AlterTable) *parser.AlterTable {
	if !slices.ContainsFunc(s.Actions, func(a parser.AlterTableAction) bool { return a.Action == parser.SetNotNull }) {
		return s
	}
	command := *s
	command.Actions = slices.Clone(s.Actions)
	for i := range command.Actions {
		if command.Actions[i].Action == parser.SetNotNull {
			a := &command.Actions[i]
			a.NoInherit = len(catalog.InheritanceChildren(view, old.Address())) != 0
			if slot, ok := old.Index[a.Column.Text]; ok {
				for _, c := range old.Constraints.NotNull {
					if c.Attribute == old.Attributes[slot] && c.NoInherit {
						a.NoInherit = true
					}
				}
			}
		}
	}
	return &command
}

// AddedUniqueSyntax normalizes the successfully added columns' UNIQUE and
// PRIMARY KEY clauses into one list. Declarations that build the same index
// collapse as PostgreSQL's transformIndexConstraints reduces them: the primary
// key is retained first, and an unnamed retained declaration takes a dropped
// declaration's name.
func AddedUniqueSyntax(additions []AddedColumn) []parser.UniqueConstraint {
	var raw []parser.UniqueConstraint
	for _, added := range additions {
		if added.Attribute == 0 {
			continue
		}
		raw = append(raw, retainAddedColumnIndexes(addedColumnIndexes(added))...)
	}
	return raw
}

func addedColumnIndexes(added AddedColumn) []parser.UniqueConstraint {
	var declarations []parser.UniqueConstraint
	for _, constraint := range added.Column.Constraints {
		if constraint.Unique == nil {
			continue
		}
		definition := *constraint.Unique // value copy; never borrow the parser AST
		definition.Columns = []parser.Identifier{{Text: added.Column.Name, Pos: added.Column.Pos}}
		declarations = append(declarations, definition)
	}
	return declarations
}

// retainAddedColumnIndexes is transformIndexConstraints for one added column.
// Every inline declaration indexes exactly this column through the default
// btree access method with no INCLUDE, WHERE or exclusion operators, so the
// index shapes compare by NullsNotDistinct/Deferrable/InitiallyDeferred alone.
// A primary key is never dropped, so a second one still reaches staging as
// 42P16 instead of being deduplicated away.
func retainAddedColumnIndexes(declarations []parser.UniqueConstraint) []parser.UniqueConstraint {
	if primary := slices.IndexFunc(declarations, func(d parser.UniqueConstraint) bool { return d.Primary }); primary > 0 {
		first := declarations[primary]
		copy(declarations[1:primary+1], declarations[:primary])
		declarations[0] = first
	}
	retained := declarations[:0]
	for _, declaration := range declarations {
		keep := true
		if !declaration.Primary {
			for i := range retained {
				prior := &retained[i]
				if prior.NullsNotDistinct != declaration.NullsNotDistinct ||
					prior.Deferrable != declaration.Deferrable ||
					prior.InitiallyDeferred != declaration.InitiallyDeferred {
					continue
				}
				if prior.Name.Text == "" {
					prior.Name = declaration.Name
				}
				keep = false
				break
			}
		}
		if keep {
			retained = append(retained, declaration)
		}
	}
	return retained
}

// AddedCheckSyntax collects the CHECKs that actions and the columns they add
// declare, in source order.
func AddedCheckSyntax(actions []parser.AlterTableAction, additions []AddedColumn) []*parser.CheckConstraint {
	var raw []*parser.CheckConstraint
	for _, action := range actions {
		if action.Action == parser.AddCheck {
			raw = append(raw, action.Check)
		}
	}
	for _, added := range additions {
		for _, constraint := range added.Column.Constraints {
			if constraint.Check != nil {
				raw = append(raw, constraint.Check)
			}
		}
	}
	if len(raw) > 1 {
		slices.SortFunc(raw, func(a, b *parser.CheckConstraint) int { return a.Pos.Offset - b.Pos.Offset })
	}
	return raw
}

// StoredReplacements are the replaced generation attributes of next that are
// stored, so their values change.
func StoredReplacements(next *catalog.Relation, replacements []int32) []int32 {
	var stored []int32
	for _, attribute := range replacements {
		if next.ColumnMeta.Generations[attribute].Kind == catalog.GenerationStored {
			stored = append(stored, attribute)
		}
	}
	return stored
}

// DependencyTargets are the columns of relation whose dependents ALTER TABLE
// observes: those dropped from old, TYPE-transformed, or with replaced
// generations.
func DependencyTargets(relation ids.ObjectAddress, old, next *catalog.Relation, transformations []index.ColumnTransformation, replacements []int32) []ids.ObjectAddress {
	var targets []ids.ObjectAddress
	column := func(attribute int32) {
		target := relation
		target.SubID = attribute
		targets = append(targets, target)
	}
	for _, attribute := range old.Attributes {
		if !next.HasAttribute(attribute) {
			column(attribute)
		}
	}
	for _, transformation := range transformations {
		column(transformation.Attribute)
	}
	for _, attribute := range replacements {
		column(attribute)
	}
	return targets
}
