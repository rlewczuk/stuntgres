package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func selectedLogPowerName(name string) bool {
	switch name {
	case "lgamma", "ln", "log", "log10", "power", "sqrt", "width_bucket":
		return true
	}
	return false
}

func (b *expressionBinder) bindMath(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.defaultConsumer && !selectedLogPowerName(name) {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	if name == "width_bucket" && len(raw.Args) == 2 {
		return b.bindThresholdBucket(raw, visible)
	}
	args, unknown, err := functions.AnalyzeDirectArguments[BoundExpr](b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if !visible {
		return nil, b.randomSignature(raw, args, unknown, false)
	}
	prepared := functions.PrepareMath(name)
	var types functions.NumericTypes
	if prepared.Needs(sqltype.Int2) {
		types.Int2 = b.typ(sqltype.Int2).Usage()
	}
	if prepared.Needs(sqltype.Int4) {
		types.Int4 = b.typ(sqltype.Int4).Usage()
	}
	if prepared.Needs(sqltype.Int8) {
		types.Int8 = b.typ(sqltype.Int8).Usage()
	}
	if prepared.Needs(sqltype.Float4) {
		types.Float4 = b.typ(sqltype.Float4).Usage()
	}
	if prepared.Needs(sqltype.Float8) {
		types.Float8 = b.typ(sqltype.Float8).Usage()
	}
	if prepared.Needs(sqltype.Numeric) {
		types.Numeric = b.typ(sqltype.Numeric).Usage()
	}
	signature, resolution := functions.SelectMath(prepared, name, raw.Variadic, args, unknown, types)
	if resolution != functions.Resolved {
		return nil, b.randomSignature(raw, args, unknown, resolution == functions.Ambiguous)
	}
	if selectedLogPowerName(name) {
		if err := mathModifiers(raw, name); err != nil {
			return nil, err
		}
	}
	if err := functions.CoerceMathArguments(b, name, raw.Args, args, unknown, signature, types); err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: Immutable}
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	usage := b.typ(signature.Result).Usage()
	node, err := functions.NewMath[BoundExpr, *EvalContext](functionInfo(b.info(usage, raw.Pos, properties)), name, args)
	if err != nil {
		return nil, err
	}
	node.WrapperContext = mathWrapperContext(name, args)
	return node, nil
}

func mathWrapperContext(name string, args []BoundExpr) string {
	if (name == "log" || name == "log10") && len(args) == 1 && args[0].ResultUsage().Type.BaseType().ID() == sqltype.Numeric && !args[0].Properties().RowDependent {
		return "SQL function \"" + name + "\" statement 1"
	}
	return ""
}

func mathModifiers(raw parser.FunctionExpr, name string) error {
	switch {
	case raw.Star:
		return expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	return nil
}

func (b *expressionBinder) bindThresholdBucket(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	operands := make([]functions.Argument[BoundExpr], len(raw.Args))
	for i, child := range raw.Args {
		operand, err := b.comparisonOperandPolicy(child, expressionPolicy{context: projectionContext, thresholdArrayInput: i == 1})
		if err != nil {
			return nil, err
		}
		operands[i] = functions.Argument[BoundExpr]{Expression: operand.node, Raw: operand.raw}
		if operand.node != nil {
			operands[i].Usage = operand.node.ResultUsage()
		}
	}
	names := make([]string, len(operands))
	for i, operand := range operands {
		names[i] = "unknown"
		if operand.Usage.Type != nil {
			names[i] = b.arraySearchTypeName(operand.Usage.Type)
		}
	}
	signature := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	if !visible || raw.Variadic || len(raw.ArgNames) != 0 {
		return signature()
	}
	var views [2]comparisonOperand
	for i, operand := range operands {
		views[i] = comparisonOperand{node: operand.Expression, raw: operand.Raw}
	}
	if operands[1].Usage.Type != nil {
		leaf, ok := functions.ArraySearchLeafUsage(operands[1].Usage)
		if !ok {
			return signature()
		}
		views[1].node = literalExpression{expressionInfo: expressionInfo{usage: leaf}}
	}
	known := views[:0]
	for _, view := range views {
		if view.descriptor() != nil {
			known = append(known, view)
		}
	}
	common, err := selectCommonUsage(len(known), func(i int) comparisonOperand { return known[i] }, b.typ(sqltype.Text).Usage(), "", b.diagnosticTypeName)
	if err != nil {
		return signature()
	}
	array, ok := b.definitionArray(common.Type)
	if !ok || !array.Concrete() {
		return signature()
	}
	targets := [2]sqltype.Usage{common, array.Usage()}
	for i, operand := range operands {
		if operand.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(operand.Usage, targets[i], sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	if err := mathModifiers(raw, "width_bucket"); err != nil {
		return nil, err
	}
	args := make([]BoundExpr, 2)
	properties := ExprProperties{Volatility: Immutable}
	for i, operand := range operands {
		intent := functions.DirectFormalArgument
		if _, parameter := operand.Raw.(parser.ParameterExpr); parameter && operand.Usage.Type == nil {
			// Preserve occurrence-sensitive inference for scalar/array formals,
			// just as for the selected fixed-signature math overloads.
			intent = functions.FinalizeArgument
		}
		args[i], err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: intent, Target: targets[i], PreserveProperties: true, AtRaw: true})
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	input, err := b.comparisonInfo(args[0], args[1])
	if err != nil {
		return nil, err
	}
	result := b.typ(sqltype.Int4).Usage()
	plan, err := sqltype.BindWidthBucketThreshold(result, args[0].ResultUsage(), args[1].ResultUsage(), input.usage.Collation, input.collationConflict)
	if err != nil {
		return nil, err
	}
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	return functions.Math[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(b.info(result, raw.Pos, properties)), Plan: plan, Args: args}, nil
}
