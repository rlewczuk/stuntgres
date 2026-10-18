package engine

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRangeInspect(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	textOverload := strings.EqualFold(name, "lower") || strings.EqualFold(name, "upper")
	var small [2]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		request := functions.AnalysisRequest{Intent: functions.ComparisonArgument}
		if textOverload {
			request = functions.StringArgumentAnalysis(name, i)
		}
		operand, err := b.AnalyzeArgument(child, request)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = operand
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	// Visibility follows ordinary/FILTER children and label/count validation.
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if textOverload {
		return b.finishStringFunction(raw, visible, operands)
	}
	return b.finishRangeInspect(raw, visible, operands)
}
func (b *expressionBinder) finishRangeInspect(raw parser.FunctionExpr, visible bool, operands []functions.Argument) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation, known := sqltype.RangeInspectKind(name)
	if visible && known && operation == sqltype.RangeConstruct && len(raw.ArgNames) == 0 && len(operands) == 1 && operands[0].Usage.Type != nil {
		switch operands[0].Usage.Type.BaseType().ID() {
		case sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange:
			return nil, expressionError("0A000", "temporal multirange construction is unsupported", raw.Pos)
		}
	}
	selected, resolution := functions.ResolveRangeInspect(operation, visible && known && len(raw.ArgNames) == 0, operands)
	if resolution != functions.Resolved {
		if resolution == functions.UnknownPolymorphic {
			return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
		}
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = "unknown"
			if typ := operand.Usage.Type; typ != nil {
				names[i] = b.diagnosticTypeName(typ)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, resolution == functions.Ambiguous)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	args, err := functions.CoerceRangeInspectArguments(b, operands, selected.Usage(), name)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(functions.RangeInspectIntrinsicRule())
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	result := sqltype.RangeInspectResult(operation, selected).Usage()
	formalType := sqltype.ID(3831) // Released ANYRANGE formal.
	switch selected.ID() {
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		formalType = 4537 // Released ANYMULTIRANGE formal.
	}
	var formal [2]sqltype.ID
	formal[0], formal[1] = formalType, formalType
	info, err := b.builtinRoutineInfo(b.info(result, raw.Pos, properties), rangeRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return functions.NewRangeInspect(info, operation, args)
}
