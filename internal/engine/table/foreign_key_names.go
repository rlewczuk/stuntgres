package table

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// ForeignKeyConstraintName is shared by binding and pre-wait ALTER admission.
// Only generated names use the namespace reservation set; explicit names retain
// their ordinary relation-local conflict check at installation.
func ForeignKeyConstraintName(child *catalog.Relation, raw parser.ForeignKeyConstraint, used map[string]bool) string {
	if raw.Name.Text != "" {
		return raw.Name.Text
	}
	var labels strings.Builder
	if len(raw.Columns) != 0 {
		size := len(raw.Columns) - 1
		for _, column := range raw.Columns {
			size += len(column.Text)
		}
		labels.Grow(size)
	}
	for i, column := range raw.Columns {
		if i != 0 {
			labels.WriteByte('_')
		}
		labels.WriteString(column.Text)
	}
	return catalog.GeneratedConstraintName(child.Name(), labels.String(), "fkey", used)
}

// retainNameFacts detaches only a slice whose removed facts affect naming.
func retainNameFacts[T any](facts []T, removed func(T) bool) []T {
	if !slices.ContainsFunc(facts, removed) {
		return facts
	}
	return slices.DeleteFunc(slices.Clone(facts), removed)
}

// AlterForeignKeyDeclaration is an owned command-local definition. ConstraintIndex
// is -1 for an explicit table FK; otherwise it identifies the inline constraint.
// Origin indexes remain in the original action list, not installation order.
type AlterForeignKeyDeclaration struct {
	Action          int
	ConstraintIndex int
	Definition      parser.ForeignKeyConstraint
	Name            string
}

func cloneForeignKeyDefinition(raw parser.ForeignKeyConstraint) parser.ForeignKeyConstraint {
	raw.Columns = slices.Clone(raw.Columns)
	raw.Target.Parts = slices.Clone(raw.Target.Parts)
	raw.Referenced = slices.Clone(raw.Referenced)
	raw.DeleteColumns = slices.Clone(raw.DeleteColumns)
	return raw
}

// AlterForeignKeyDeclarations projects admissions and names without binding,
// notices, OIDs or descriptor/AST mutation. DROP precedes ADD; an existing
// IF NOT EXISTS column contributes no subsidiary declarations or reservations.
// Inline CHECK names precede inline FKs, which precede explicit CHECK/FK names.
func AlterForeignKeyDeclarations(view catalog.Read, child *catalog.Relation, actions []parser.AlterTableAction) []AlterForeignKeyDeclaration {
	var droppedNames map[string]bool
	var droppedAttributes map[int32]bool
	var droppedNotNull map[ids.OID]bool
	var droppedColumns []index.ColumnTransformation
	count := 0
	for _, action := range actions {
		switch action.Action {
		case parser.AddForeignKey:
			count++
		case parser.AddColumn:
			for _, constraint := range action.Definition.Constraints {
				if constraint.ForeignKey != nil {
					count++
				}
			}
		case parser.DropConstraint:
			if droppedNames == nil {
				droppedNames = make(map[string]bool)
			}
			droppedNames[action.Constraint.Text] = true
		case parser.DropNotNull:
			if _, fact := selectNotNullConstraint(child, action); fact >= 0 {
				if droppedNotNull == nil {
					droppedNotNull = make(map[ids.OID]bool)
				}
				droppedNotNull[child.Constraints.NotNull[fact].OID] = true
			}
		case parser.DropColumn:
			if slot, ok := child.Index[action.Column.Text]; ok {
				attribute := child.Attributes[slot]
				if droppedAttributes == nil {
					droppedAttributes = make(map[int32]bool)
				}
				if !droppedAttributes[attribute] {
					droppedAttributes[attribute] = true
					droppedColumns = append(droppedColumns, index.ColumnTransformation{Attribute: attribute})
				}
			}
		}
	}
	if count == 0 {
		return nil
	}
	admitted, admissible := alterAddedColumnAdmissions(child, actions, droppedAttributes)
	if !admissible {
		return nil
	}
	projected := *child
	removed := func(name string, attributes []int32) bool {
		if droppedNames[name] {
			return true
		}
		return slices.ContainsFunc(attributes, func(attribute int32) bool { return droppedAttributes[attribute] })
	}
	if len(droppedNames)+len(droppedAttributes)+len(droppedNotNull) != 0 {
		projected.Constraints.NotNull = retainNameFacts(child.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
			return droppedNames[c.Name] || droppedAttributes[c.Attribute] || droppedNotNull[c.OID]
		})
		projected.Constraints.Check = retainNameFacts(child.Constraints.Check, func(c catalog.CheckConstraint) bool {
			if droppedNames[c.Name] {
				return true
			}
			for _, dependency := range c.Dependencies {
				address := dependency.Ref.Address
				attribute := address.SubID
				address.SubID = 0
				if address == child.Address() && droppedAttributes[attribute] {
					return true
				}
			}
			return false
		})
		projected.Constraints.Unique = retainNameFacts(child.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return removed(c.Name, c.Attributes) })
		projected.Constraints.Exclusion = retainNameFacts(child.Constraints.Exclusion, func(c catalog.ExclusionConstraint) bool {
			if droppedNames[c.Name] {
				return true
			}
			idx, ok := view.Relation(c.Index)
			return ok && idx.SQLIndex != nil && index.UniqueIndexTypeAffected(idx.SQLIndex, droppedColumns)
		})
		projected.Constraints.ForeignKeys = retainNameFacts(child.Constraints.ForeignKeys, func(c catalog.ForeignKeyConstraint) bool {
			return removed(c.Name, c.Attributes) || c.Target == child.Address() && removed("", c.Referenced)
		})
	}
	// Replacing just this relation preserves same-name reservations belonging
	// to other relations/domains in the namespace.
	used := catalog.ConstraintNames(view, child.Namespace(), &projected)
	for i, action := range actions {
		name := ""
		switch action.Action {
		case parser.AddNotNull:
			name = action.Constraint.Text
		case parser.AddUnique:
			name = action.Unique.Name.Text
		case parser.AddExclusion:
			name = action.Exclusion.Name.Text
		case parser.AddUsingIndex:
			name = action.UsingIndex.Name.Text
			if name == "" {
				name = action.UsingIndex.Index.Text
			}
		case parser.AddColumn:
			if !admitted[i] {
				continue
			}
			for _, constraint := range action.Definition.Constraints {
				if constraint.NotNull && constraint.Name.Text != "" {
					used[constraint.Name.Text] = true
				}
				if constraint.Unique != nil && constraint.Unique.Name.Text != "" {
					used[constraint.Unique.Name.Text] = true
				}
			}
		}
		// Newly generated non-FK names have check/not_null/key/pkey/excl
		// labels, so cannot equal an fkey name, including after truncation.
		if name != "" {
			used[name] = true
		}
	}
	declarations := make([]AlterForeignKeyDeclaration, 0, count)
	appendDeclaration := func(action, constraint int, raw parser.ForeignKeyConstraint) {
		raw = cloneForeignKeyDefinition(raw)
		if constraint >= 0 {
			column := actions[action].Definition
			raw.Columns = []parser.Identifier{{Text: column.Name, Pos: column.Pos}}
		}
		name := ForeignKeyConstraintName(child, raw, used)
		declarations = append(declarations, AlterForeignKeyDeclaration{Action: action, ConstraintIndex: constraint, Definition: raw, Name: name})
		used[name] = true
	}
	// All effective inline CHECK names reserve before any inline generated FK,
	// independently of the within-column spelling.
	for i, action := range actions {
		if action.Action != parser.AddColumn || !admitted[i] {
			continue
		}
		for _, constraint := range action.Definition.Constraints {
			if constraint.Check != nil && constraint.Check.Name.Text != "" {
				used[constraint.Check.Name.Text] = true
			}
		}
	}
	for i, action := range actions {
		if action.Action != parser.AddColumn || !admitted[i] {
			continue
		}
		for j, constraint := range action.Definition.Constraints {
			if constraint.ForeignKey != nil {
				appendDeclaration(i, j, *constraint.ForeignKey)
			}
		}
	}
	for i, action := range actions {
		switch action.Action {
		case parser.AddCheck:
			if name := action.Check.Name.Text; name != "" {
				used[name] = true
			}
		case parser.AddForeignKey:
			appendDeclaration(i, -1, *action.ForeignKey)
		}
	}
	if len(declarations) == 0 {
		return nil
	}
	return declarations
}
