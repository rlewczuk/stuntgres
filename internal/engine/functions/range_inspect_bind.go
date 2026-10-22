package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// rangeInspectNames are the range inspection and hull functions; lower and
// upper share text overloads and stay with the string family.
var rangeInspectNames = [...]string{"isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge", "multirange"}

// bindRangeInspect binds the range and multirange inspection and hull
// functions. Visibility follows ordinary/FILTER children and label/count
// validation.
func bindRangeInspect(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]Argument
	var operands []Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = operand
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return FinishRangeInspect(b, raw, visible, operands)
}

// FinishRangeInspect resolves and binds a range inspection call over
// analyzed operands. The string family reaches it for lower/upper of a
// range or multirange.
func FinishRangeInspect(b Binder, raw parser.FunctionExpr, visible bool, operands []Argument) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation, known := sqltype.RangeInspectKind(name)
	if visible && known && operation == sqltype.RangeConstruct && len(raw.ArgNames) == 0 && len(operands) == 1 && operands[0].Usage.Type != nil {
		switch operands[0].Usage.Type.BaseType().ID() {
		case sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange:
			return nil, expr.ErrorAt("0A000", "temporal multirange construction is unsupported", raw.Pos)
		}
	}
	selected, resolution := ResolveRangeInspect(operation, visible && known && len(raw.ArgNames) == 0, operands)
	if resolution != Resolved {
		if resolution == UnknownPolymorphic {
			return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
		}
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = "unknown"
			if typ := operand.Usage.Type; typ != nil {
				names[i] = b.DiagnosticTypeName(typ)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, UnnotedResolutionError(b, raw.Name, raw.Pos, names, resolution == Ambiguous)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	args, err := CoerceRangeInspectArguments(b, operands, selected.Usage(), name)
	if err != nil {
		return nil, err
	}
	properties := expr.IntrinsicProperties(RangeInspectIntrinsicRule())
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	result := sqltype.RangeInspectResult(operation, selected).Usage()
	formalType := rangeCallFormal(selected)
	formal := [2]sqltype.ID{formalType, formalType}
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), RangeRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return NewRangeInspect(info, operation, args)
}
