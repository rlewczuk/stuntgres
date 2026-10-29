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
		case parser.SetStorage:
			return true
		case parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
			return true
		}
	}
	return false
}

func InheritancePureDefinitionPreparation(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AddIdentity, parser.SetIdentity, parser.AddUsingIndex:
		default:
			return false
		}
	}
	return true
}
