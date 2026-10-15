package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) resolveNtileSignature(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [1]functions.Argument[BoundExpr]
	var names [1]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 1 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]functions.Argument[BoundExpr], len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		argument, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arguments[i] = argument
		types[i] = "unknown"
		if argument.Usage.Type != nil {
			types[i] = b.diagnosticTypeName(argument.Usage.Type)
		}
	}
	if _, err := b.bindAggregateFilter(raw); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	var usage sqltype.Usage
	if len(arguments) > 0 {
		usage = arguments[0].Usage
	}
	integer := b.typ(sqltype.Int4).Usage()
	decision, plan := functions.ResolveNtile(visible, raw.Star, len(arguments), usage, integer)
	if decision == functions.Missing {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	return b.CoerceArgument(arguments[0], functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: integer, AtRaw: true, Plan: plan, Planned: usage.Type != nil})
}
