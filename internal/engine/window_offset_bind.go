package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func windowOffsetName(name string) bool { return name == "lead" || name == "lag" }

func (b *expressionBinder) resolveWindowOffsetSignature(raw parser.AggregateExpr, name parser.ObjectName) (value, offset, fallback BoundExpr, usage sqltype.Usage, err error) {
	var small [3]functions.Argument[BoundExpr]
	var names [3]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 3 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]functions.Argument[BoundExpr], len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arguments[i], err = b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return
		}
		types[i] = "unknown"
		if typ := arguments[i].Usage.Type; typ != nil {
			types[i] = b.diagnosticTypeName(typ)
		}
	}
	if _, err = b.bindAggregateFilter(raw); err != nil {
		return
	}
	if len(raw.Args) > 100 {
		err = expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
		return
	}
	visible, failure := b.builtinVisible(name, functionLookup)
	if failure != nil {
		err = failure
		return
	}
	var valueUsage, offsetUsage sqltype.Usage
	if len(arguments) > 0 {
		valueUsage = arguments[0].Usage
	}
	if len(arguments) > 1 {
		offsetUsage = arguments[1].Usage
	}
	integer := b.typ(sqltype.Int4).Usage()
	decision, offsetPlan := functions.ResolveWindowOffset(visible, raw.Star, len(arguments), valueUsage, offsetUsage, integer)
	if decision == functions.Missing {
		err = functionSignatureErrorForTypes(name, raw.Pos, types)
		return
	}
	if decision == functions.UnknownPolymorphic {
		err = sqlError("42804", "could not determine polymorphic type because input has type unknown")
		return
	}
	usage = valueUsage
	if len(arguments) == 3 {
		var known [2]comparisonOperand
		count := 0
		for _, i := range [2]int{0, 2} {
			if arguments[i].Usage.Type != nil {
				known[count] = comparisonOperand{node: arguments[i].Expression, raw: arguments[i].Raw}
				count++
			}
		}
		usage, err = selectCommonUsage(count, func(i int) comparisonOperand { return known[i] }, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
		if err != nil {
			return
		}
		if usage.Type == nil {
			err = functionSignatureErrorForTypes(name, raw.Pos, types)
			return
		}
		for _, i := range [2]int{0, 2} {
			if arguments[i].Usage.Type != nil {
				if _, failure := sqltype.PlanCoercion(arguments[i].Usage, usage, sqltype.Implicit); failure != nil {
					err = functionSignatureErrorForTypes(name, raw.Pos, types)
					return
				}
			}
		}
	}
	// Coerce in argument order, so reused parameter occurrence diagnostics agree
	// with the formal value, INT4 offset, and common default types.
	if len(arguments) == 3 {
		value, err = b.CoerceArgument(arguments[0], functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: usage})
	} else {
		value = arguments[0].Expression
	}
	if err != nil {
		return
	}
	if len(arguments) > 1 {
		offset, err = b.CoerceArgument(arguments[1], functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: integer, AtRaw: true, Plan: offsetPlan, Planned: offsetUsage.Type != nil})
		if err != nil {
			return
		}
	}
	if len(arguments) == 3 {
		fallback, err = b.CoerceArgument(arguments[2], functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: usage})
		if err != nil {
			return
		}
	}
	usage.Typmod = -1
	return
}
