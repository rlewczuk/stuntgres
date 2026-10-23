package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindGeometryMeasure(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [1]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	missing := func(ambiguous bool) (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	if !visible || raw.Star || len(raw.ArgNames) != 0 || raw.Variadic || len(operands) != 1 {
		return missing(false)
	}
	var selected sqltype.GeometryMeasure
	matches, best := 0, -1
	for op := sqltype.AreaBox; op <= sqltype.DiameterCircle; op++ {
		if op.Name() != name {
			continue
		}
		exact := 0
		arg := operands[0]
		if arg.Usage.Type != nil {
			target := b.Type(op.Formal()).Usage()
			if arg.Usage == target {
				exact = 1
			} else if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
				continue
			}
		}
		if exact > best {
			selected, best, matches = op, exact, 1
		} else if exact == best {
			matches++
		}
	}
	if matches != 1 {
		return missing(matches > 1)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: ConditionalArgument, Target: b.Type(selected.Formal()).Usage(), Label: name})
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(selected.ResultID()).Usage(), raw.Pos, properties), GeometryMeasureRoutineProvider, name, selected.Formal())
	if err != nil {
		return nil, err
	}
	return NewGeometryMeasure(info, selected, arg, "pg_catalog")
}
