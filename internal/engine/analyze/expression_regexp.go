package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func (b *Binder) regexpPlacement(call functions.RegexpCall) error {
	context := b.Clause.RegexpClause
	hint := context == "CASE" || context == "COALESCE"
	if context == "" && b.Grouping.ArgumentOwner.Active {
		message := "aggregate function calls cannot contain set-returning function calls"
		if b.Grouping.ArgumentOwner.Window {
			message = "window function calls cannot contain set-returning function calls"
		}
		err := expr.ErrorAt("0A000", message, call.SourcePosition()).(*sqlstate.Error)
		err.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		return err
	}
	if context == "" {
		switch {
		case b.Params.ExecuteParameters:
			context = "EXECUTE parameters"
		case b.Target.GeneratedOwner != nil:
			context = "column generation expressions"
		case b.Consumer.IndexPredicateConsumer:
			context = "index predicates"
		case b.Consumer.IndexConsumer:
			context = "index expressions"
		case b.Consumer.CheckConsumer:
			context = "check constraints"
		case b.DefaultConsumer():
			context = "DEFAULT expressions"
		case b.Consumer.PolicyConsumer && b.Query.Frame == 0:
			return sqlError("0A000", "set-returning functions are not allowed in policy expressions")
		default:
			switch b.Clause.AggregateClause {
			case "WHERE", "HAVING", "FILTER", "JOIN conditions", "LIMIT", "OFFSET", "UPDATE", "RETURNING", "VALUES", "window RANGE", "window ROWS", "window GROUPS":
				context = b.Clause.AggregateClause
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
