package table

import (
	"stuntgres/internal/parser"
)

func InheritanceEntryOwner(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.AlterConstraintInherit:
			if !action.NoInherit {
				return true
			}
		case parser.SetStorage, parser.SetColumnStatistics:
			return true
		case parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
			return true
		}
	}
	return false
}

// inlineUniqueAddition reports whether a statement adds a column carrying an
// inline UNIQUE or PRIMARY KEY constraint. Such a key stages its index in the
// sole physical pipeline exactly like a table-level ADD UNIQUE, so it must not
// use a node cooked before that stage exists.
func inlineUniqueAddition(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		if action.Action != parser.AddColumn || action.Definition == nil {
			continue
		}
		for _, constraint := range action.Definition.Constraints {
			if constraint.Unique != nil {
				return true
			}
		}
	}
	return false
}

func InheritancePureDefinitionPreparation(actions []parser.AlterTableAction) bool {
	// Inline keys need the sole index stage just like ADD UNIQUE.
	if inlineUniqueAddition(actions) {
		return false
	}
	for _, action := range actions {
		switch action.Action {
		case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AddIdentity, parser.SetIdentity, parser.AddUsingIndex:
		default:
			return false
		}
	}
	return true
}
