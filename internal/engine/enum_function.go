package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindEnumFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operands := make([]functions.Argument[BoundExpr], len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, r := range raw.Args {
		operand, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
		names[i] = "unknown"
		if typ := operand.Usage.Type; typ != nil {
			names[i] = b.diagnosticTypeName(typ)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	selection, resolution := functions.ResolveEnum(name, visible, operands)
	if resolution != functions.Resolved {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	selected := selection.Descriptor
	args, err := functions.CoerceEnumArguments(b, operands, selected.Usage(), name)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(functions.EnumIntrinsicRule())
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if err := b.captureTypeDiagnosticInput(selected); err != nil {
		return nil, err
	}
	result := selected.Usage()
	if selection.Range {
		array, ok := b.definitionArray(selected)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing enum array association")
		}
		result = array.Usage()
	}
	info := b.info(result, raw.Pos, properties)
	node, err := functions.NewEnum[BoundExpr, ExprContext](functionInfo(info), name, args, b.diagnosticTypeName(selected))
	if err != nil {
		return nil, err
	}
	formal := [2]sqltype.ID{sqltype.ID(3500), sqltype.ID(3500)}
	info, err = b.builtinRoutineInfo(info, enumRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	node.Info = functionInfo(info)
	return node, nil
}
