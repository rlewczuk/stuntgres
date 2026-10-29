package table

import (
	"stuntgres/internal/parser"
)

// Both preparation and consumption use the incumbent physical phase ordering.
func AlterActionPhase(action parser.AlterTableActionKind) int {
	switch action {
	case parser.SetStorage, parser.SetCompression, parser.SetReplicaIdentity, parser.SetColumnStatistics, parser.SetColumnOptions, parser.ResetColumnOptions:
		return 10
	case parser.SetTablespace:
		// Root destination preparation consumes unchanged placement before phases.
		return -1
	case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.DropExpression, parser.SetWithoutOIDs:
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
	case parser.InheritTable, parser.NoInheritTable, parser.SetRelationOptions, parser.ResetRelationOptions:
		return 9
	case parser.AlterConstraint, parser.AlterConstraintInherit, parser.AlterConstraintEnforcement, parser.AlterConstraintTiming, parser.ValidateConstraint, parser.EnableRule, parser.DisableRule, parser.EnableReplicaRule, parser.EnableAlwaysRule, parser.EnableTrigger, parser.DisableTrigger, parser.EnableReplicaTrigger, parser.EnableAlwaysTrigger:
		return 10
	case parser.SetLogged, parser.SetUnlogged, parser.SetAccessMethod, parser.ClusterOn, parser.SetWithoutCluster:
		// Misc is consumed after the deferred FOREIGN KEY definitions exist.
		return 10
	}
	return -1
}

func inheritanceActionPass(action parser.AlterTableActionKind) int {
	switch action {
	case parser.SetStorage, parser.SetCompression, parser.SetReplicaIdentity, parser.SetColumnStatistics, parser.SetColumnOptions, parser.ResetColumnOptions:
		return 10
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
	case parser.AlterConstraint, parser.AlterConstraintInherit, parser.AlterConstraintEnforcement, parser.AlterConstraintTiming, parser.ValidateConstraint, parser.EnableRule, parser.DisableRule, parser.EnableReplicaRule, parser.EnableAlwaysRule, parser.EnableTrigger, parser.DisableTrigger, parser.EnableReplicaTrigger, parser.EnableAlwaysTrigger:
		return 10
	default:
		return 8
	}
}
