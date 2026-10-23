package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindEnumFunction(raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands := make([]functions.Argument, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		names[i] = "unknown"
		if typ := operand.Usage.Type; typ != nil {
			names[i] = b.DiagnosticTypeName(typ)
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	selection, resolution := functions.ResolveEnum(name, visible, operands)
	if resolution != functions.Resolved {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	switch {
	case raw.Star:
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expr.ErrorAt("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	selected := selection.Descriptor
	args, err := functions.CoerceEnumArguments(b, operands, selected.Usage(), name)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(functions.EnumIntrinsicRule())
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := b.CaptureTypeDiagnosticInput(selected); err != nil {
		return nil, err
	}
	result, ok := selection.Result(b)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing enum array association")
	}
	info := b.Info(result, raw.Pos, properties)
	node, err := functions.NewEnum(info, name, args, b.DiagnosticTypeName(selected))
	if err != nil {
		return nil, err
	}
	formal := [2]sqltype.ID{sqltype.ID(3500), sqltype.ID(3500)}
	info, err = b.builtinRoutineInfo(info, functions.EnumRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
