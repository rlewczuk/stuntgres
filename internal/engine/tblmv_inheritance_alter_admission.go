package engine

import (
	"stuntgres/internal/parser"
)

func inheritanceEntryOwner(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
			return true
		}
	}
	return false
}

func inheritancePureDefinitionPreparation(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AddIdentity, parser.SetIdentity, parser.AddUsingIndex:
		default:
			return false
		}
	}
	return true
}
