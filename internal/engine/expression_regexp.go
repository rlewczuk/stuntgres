package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func (b *expressionBinder) regexpPlacement(call functions.RegexpCall) error {
	context := b.clause.regexpClause
	hint := context == "CASE" || context == "COALESCE"
	if context == "" && b.grouping.argumentOwner.active {
		message := "aggregate function calls cannot contain set-returning function calls"
		if b.grouping.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		err := expr.ErrorAt("0A000", message, call.SourcePosition()).(*sqlstate.Error)
		err.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		return err
	}
	if context == "" {
		switch {
		case b.params.executeParameters:
			context = "EXECUTE parameters"
		case b.target.generatedOwner != nil:
			context = "column generation expressions"
		case b.consumer.indexPredicateConsumer:
			context = "index predicates"
		case b.consumer.indexConsumer:
			context = "index expressions"
		case b.consumer.checkConsumer:
			context = "check constraints"
		case b.DefaultConsumer():
			context = "DEFAULT expressions"
		case b.consumer.policyConsumer && b.query.frame == 0:
			return sqlError("0A000", "set-returning functions are not allowed in policy expressions")
		default:
			switch b.clause.aggregateClause {
			case "WHERE", "HAVING", "FILTER", "JOIN conditions", "LIMIT", "OFFSET", "UPDATE", "RETURNING", "VALUES", "window RANGE", "window ROWS", "window GROUPS":
				context = b.clause.aggregateClause
				if context == "window RANGE" || context == "window ROWS" || context == "window GROUPS" {
					context = "window definitions"
				}
			}
		}
	}
	if context == "" {
		return nil
	}
	return rejectRegexpSets(functions.RegexpSet{RegexpCall: call}, context, hint)
}
