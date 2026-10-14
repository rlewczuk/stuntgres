package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRangeInspect(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	textOverload := strings.EqualFold(name, "lower") || strings.EqualFold(name, "upper")
	var small [2]functions.Argument[BoundExpr]
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
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
	if err := b.validateRangeInspectSyntax(raw); err != nil {
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
func (b *expressionBinder) finishRangeInspect(raw parser.FunctionExpr, visible bool, operands []functions.Argument[BoundExpr]) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation, known := sqltype.RangeInspectKind(name)
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
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, resolution == functions.Ambiguous)
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	args, err := functions.CoerceRangeInspectArguments(b, operands, selected.Usage(), name)
	if err != nil {
		return nil, err
	}
	properties := functionIntrinsicProperties(functions.RangeInspectIntrinsicRule())
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	result := sqltype.RangeInspectResult(operation, selected).Usage()
	return functions.NewRangeInspect[BoundExpr, *EvalContext](functionInfo(b.info(result, raw.Pos, properties)), operation, args)
}

// The selected text/range overloads share diagnostic ordering, not coercion
// rules. This envelope is reached only after ordinary children are analyzed.
func (b *expressionBinder) validateRangeInspectSyntax(raw parser.FunctionExpr) error {
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return err
	}
	if len(raw.Args) > 100 {
		return expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	return nil
}

func rangeInspectDecorationError(raw parser.FunctionExpr) error {
	decoration, kind := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, kind = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		return expressionError("42809", decoration+" specified, but "+objectNameText(raw.Name)+" is "+kind, raw.Pos)
	}
	return nil
}
