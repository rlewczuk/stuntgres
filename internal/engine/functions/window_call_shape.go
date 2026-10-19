package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func probeWindowValue(b CallTypeContext, raw parser.FunctionExpr, arguments []Argument, declared, targets []sqltype.Usage) bool {
	var value, ordinal sqltype.Usage
	if len(arguments) > 0 {
		value = arguments[0].Usage
	}
	if len(arguments) > 1 {
		ordinal = arguments[1].Usage
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	decision, _ := ResolveWindowValue(true, raw.Star, name == "nth_value", len(arguments), value, ordinal, b.Type(sqltype.Int4).Usage())
	if decision == Missing {
		return false
	}
	targets[0] = value
	return true
}

// windowOffsetTargets is the existing lag/lead signature and common-default
// selection, shared by candidate applicability and the selected signature owner.
// It never binds or imports unknown operands.
func windowOffsetTargets(b CallTypeContext, arguments []Argument, visible, star bool) (usage sqltype.Usage, offsetPlan sqltype.CoercionPlan, decision Resolution) {
	var value, offset sqltype.Usage
	if len(arguments) > 0 {
		value = arguments[0].Usage
	}
	if len(arguments) > 1 {
		offset = arguments[1].Usage
	}
	decision, offsetPlan = ResolveWindowOffset(visible, star, len(arguments), value, offset, b.Type(sqltype.Int4).Usage())
	if decision != Resolved {
		return value, offsetPlan, decision
	}
	usage = value
	if len(arguments) == 3 {
		var known [2]sqltype.Usage
		count := 0
		for _, i := range [2]int{0, 2} {
			if arguments[i].Usage.Type != nil {
				known[count] = arguments[i].Usage
				count++
			}
		}
		usage = b.CommonUsage(known[:count])
		if usage.Type == nil {
			return usage, offsetPlan, Missing
		}
		for _, i := range [2]int{0, 2} {
			if arguments[i].Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arguments[i].Usage, usage, sqltype.Implicit); err != nil {
					return usage, offsetPlan, Missing
				}
			}
		}
	}
	return usage, offsetPlan, Resolved
}

func probeWindowOffset(b CallTypeContext, raw parser.FunctionExpr, arguments []Argument, declared, targets []sqltype.Usage) bool {
	usage, _, decision := windowOffsetTargets(b, arguments, true, raw.Star)
	if decision == Missing {
		return false
	}
	targets[0] = usage
	if len(arguments) == 3 {
		targets[2] = usage
	}
	return true
}
