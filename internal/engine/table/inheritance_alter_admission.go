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

func InheritancePureDefinitionPreparation(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		if action.Action == parser.AddColumn && action.Definition != nil {
			for _, constraint := range action.Definition.Constraints {
				if constraint.Unique != nil {
					// Inline keys need the sole index stage just like ADD UNIQUE.
					return false
				}
			}
		}
		switch action.Action {
		case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AddIdentity, parser.SetIdentity, parser.AddUsingIndex:
		default:
			return false
		}
	}
	return true
}
