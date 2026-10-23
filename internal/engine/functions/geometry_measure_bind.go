package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindGeometryMeasure(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]Argument
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
	return FinishGeometryMeasure(b, raw, operands)
}

// FinishGeometryMeasure reuses analyzed operands, including shared-name length.
func FinishGeometryMeasure(b Binder, raw parser.FunctionExpr, operands []Argument) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
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
	if !visible || raw.Star || len(raw.ArgNames) != 0 {
		return missing(false)
	}
	var selected sqltype.GeometryMeasure
	matches, best := 0, -1
	for op := sqltype.AreaBox; op <= sqltype.NPointsPolygon; op++ {
		if op.Name() != name || op.Arity() != len(operands) {
			continue
		}
		exact, applicable := 0, true
		for i, arg := range operands {
			if arg.Usage.Type == nil {
				continue
			}
			target := b.Type(op.Formal(i)).Usage()
			if arg.Usage == target {
				exact++
			} else if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
				applicable = false
				break
			}
		}
		if !applicable {
			continue
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
	var args [2]expr.Expr
	var formals [2]sqltype.ID
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, operand := range operands {
		formals[i] = selected.Formal(i)
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(selected.ResultID()).Usage(), raw.Pos, properties), GeometryMeasureRoutineProvider, name, formals[:selected.Arity()]...)
	if err != nil {
		return nil, err
	}
	return NewGeometryMeasure(info, selected, args, "pg_catalog")
}
