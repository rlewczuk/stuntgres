package engine

import (
	"stuntgres/internal/parser"
)

// Both preparation and consumption use the incumbent physical phase ordering.
func alterActionPhase(action parser.AlterTableActionKind) int {
	switch action {
	case parser.SetTablespace:
		// Root destination preparation consumes unchanged placement before phases.
		return -1
	case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.DropExpression:
		return 0
	case parser.AlterColumnType:
		return 1
	case parser.AddColumn:
		return 2
	case parser.SetExpression:
		return 3
	case parser.SetNotNull:
		return 4
	case parser.AddUnique, parser.AddExclusion, parser.AddUsingIndex:
		return 5
	case parser.AddNotNull:
		return 6
	case parser.AddIdentity:
		return 7
	case parser.SetIdentity:
		return 8
	}
	return -1
}

func inheritanceActionPass(action parser.AlterTableActionKind) int {
	switch action {
	case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.DropExpression:
		return 0
	case parser.AlterColumnType:
		return 1
	case parser.AddColumn:
		return 2
	case parser.SetExpression:
		return 3
	case parser.SetNotNull:
		return 4
	case parser.AddUnique, parser.AddExclusion, parser.AddUsingIndex:
		return 5
	case parser.AddNotNull, parser.AddCheck:
		return 6
	case parser.AddIdentity:
		return 7
	default:
		return 8
	}
}
