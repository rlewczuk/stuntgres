package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindRangeInspect binds the range and multirange inspection and hull
// functions. Visibility follows ordinary/FILTER children and label/count
// validation.
func bindRangeInspect(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
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
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return finishRangeInspect(b, raw, visible, operands)
}

// finishRangeInspect resolves and binds a range inspection call over
// analyzed operands. The string family reaches it for lower/upper of a
// range or multirange.
func finishRangeInspect(b functions.Binder, raw parser.FunctionExpr, visible bool, operands []functions.Argument) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation, known := sqltype.RangeInspectKind(name)
	if visible && known && operation == sqltype.RangeConstruct && len(raw.ArgNames) == 0 && len(operands) == 1 && operands[0].Usage.Type != nil {
		switch operands[0].Usage.Type.BaseType().ID() {
		case sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange:
			return nil, expr.ErrorAt("0A000", "temporal multirange construction is unsupported", raw.Pos)
		}
	}
	selected, resolution := functions.ResolveRangeInspect(operation, visible && known && len(raw.ArgNames) == 0, operands)
	if resolution != functions.Resolved {
		if resolution == functions.UnknownPolymorphic {
			return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
		}
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = "unknown"
			if typ := operand.Usage.Type; typ != nil {
				names[i] = b.DiagnosticTypeName(typ)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		return nil, functions.ResolutionError(raw.Name, raw.Pos, names, resolution == functions.Ambiguous)
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
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	result := sqltype.RangeInspectResult(operation, selected).Usage()
	formalType := sqltype.ID(3831) // Released ANYRANGE formal.
	switch selected.ID() {
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		formalType = 4537 // Released ANYMULTIRANGE formal.
	}
	formal := [2]sqltype.ID{formalType, formalType}
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), functions.RangeRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return functions.NewRangeInspect(info, operation, args)
}
