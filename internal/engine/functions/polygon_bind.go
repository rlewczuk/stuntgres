package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindPolygon(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op, _ := sqltype.PolygonKind(name)
	left, right := op.Signature()
	formals := [2]sqltype.ID{left, right}
	var operands [2]Argument
	var names []string
	if len(raw.Args) != 2 {
		names = make([]string, len(raw.Args))
	}
	valid := len(raw.Args) == 2 && len(raw.ArgNames) == 0 && !raw.Variadic
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 2 {
			operands[i] = arg
		} else {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	valid = valid && visible
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(formals[i]).Usage(), sqltype.Implicit); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		if len(raw.Args) == 2 {
			names = make([]string, 2)
			for i, arg := range operands {
				names[i] = "unknown"
				if arg.Usage.Type != nil {
					names[i] = b.DiagnosticTypeName(arg.Usage.Type)
				}
				names[i] = NamedArgumentType(raw, i, names[i])
			}
		}
		return nil, ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), OperatorRoutineProvider, name, formals[:]...)
	if err != nil {
		return nil, err
	}
	return NewPolygon(info, op, args, "pg_catalog", true)
}
