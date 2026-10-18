package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindUUIDExtract(raw parser.FunctionExpr) (BoundExpr, error) {
	var small [1]functions.Argument[BoundExpr]
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= 1 {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	// The existing scalar syntax envelope owns child/FILTER/count/label order.
	if err := b.validateRangeInspectSyntax(raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := functions.UUIDExtractFunction(name)
	valid := visible && kind != 0 && len(operands) == 1 && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid && operands[0].Usage.Type != nil {
		valid = operands[0].Usage.Type.BaseType() == b.typ(sqltype.UUID)
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.diagnosticTypeName(arg.Usage.Type)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: b.typ(sqltype.UUID).Usage(), Label: name})
	if err != nil {
		return nil, err
	}
	properties := mergeExpressionProperties(functionIntrinsicProperties(functions.UUIDExtractIntrinsicRule()), arg.Properties())
	return functions.NewUUIDExtract[BoundExpr, ExprContext](functionInfo(b.info(b.typ(kind.Result()).Usage(), raw.Pos, properties)), kind, arg)
}
