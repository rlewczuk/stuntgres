package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func regexpSQL(call functions.RegexpCall, name string, render func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	text := name + "("
	for i, slot := range call.WrittenSlots[:len(call.Args)] {
		if i > 0 {
			text += ", "
		}
		if call.Labels[i] != "" {
			text += identifier(call.Labels[i]) + " => "
		}
		s, err := render(call.Args[slot])
		if err != nil {
			return "", err
		}
		text += s
	}
	return text + ")", nil
}

func preflightRegexpCall(c *EvalContext, call functions.RegexpCall, decisive bool) (functions.RegexpCall, bool, error) {
	var args []expr.Expr
	for i, old := range call.Args {
		next, did, err := preflightCoercionsWithPolicy(c, old, decisive)
		if err != nil {
			return call, false, err
		}
		if did {
			if args == nil {
				args = append([]expr.Expr(nil), call.Args...)
			}
			args[i] = next
		}
	}
	if args != nil {
		call.Args = args
	}
	return call, args != nil, nil
}
func sameRegexpCall(a, b functions.RegexpCall, same func(expr.Expr, expr.Expr) bool) bool {
	if a.Plan != b.Plan || len(a.Args) != len(b.Args) {
		return false
	}
	for i, arg := range a.Args {
		if !same(arg, b.Args[i]) {
			return false
		}
	}
	return true
}

func (b *expressionBinder) regexpPlacement(call functions.RegexpCall) error {
	context := b.regexpClause
	hint := context == "CASE" || context == "COALESCE"
	if context == "" && b.argumentOwner.active {
		message := "aggregate function calls cannot contain set-returning function calls"
		if b.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		err := expr.ErrorAt("0A000", message, call.SourcePosition()).(*sqlstate.Error)
		err.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		return err
	}
	if context == "" {
		switch {
		case b.executeParameters:
			context = "EXECUTE parameters"
		case b.generatedOwner != nil:
			context = "column generation expressions"
		case b.indexPredicateConsumer:
			context = "index predicates"
		case b.indexConsumer:
			context = "index expressions"
		case b.checkConsumer:
			context = "check constraints"
		case b.DefaultConsumer():
			context = "DEFAULT expressions"
		case b.policyConsumer && b.queryFrame == 0:
			return sqlError("0A000", "set-returning functions are not allowed in policy expressions")
		default:
			switch b.aggregateClause {
			case "WHERE", "HAVING", "FILTER", "JOIN conditions", "LIMIT", "OFFSET", "UPDATE", "RETURNING", "VALUES", "window RANGE", "window ROWS", "window GROUPS":
				context = b.aggregateClause
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
