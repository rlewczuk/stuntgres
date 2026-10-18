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

func bindMath(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.DefaultConsumer() && !selectedLogPowerName(name) {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	if name == "width_bucket" && len(raw.Args) == 2 {
		return bindThresholdBucket(b, raw, visible)
	}
	args, unknown, err := functions.AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if !visible {
		return nil, randomSignature(b, raw, args, unknown, false)
	}
	prepared := functions.PrepareMath(name)
	var types functions.NumericTypes
	if prepared.Needs(sqltype.Int2) {
		types.Int2 = b.Type(sqltype.Int2).Usage()
	}
	if prepared.Needs(sqltype.Int4) {
		types.Int4 = b.Type(sqltype.Int4).Usage()
	}
	if prepared.Needs(sqltype.Int8) {
		types.Int8 = b.Type(sqltype.Int8).Usage()
	}
	if prepared.Needs(sqltype.Float4) {
		types.Float4 = b.Type(sqltype.Float4).Usage()
	}
	if prepared.Needs(sqltype.Float8) {
		types.Float8 = b.Type(sqltype.Float8).Usage()
	}
	if prepared.Needs(sqltype.Numeric) {
		types.Numeric = b.Type(sqltype.Numeric).Usage()
	}
	signature, resolution := functions.SelectMath(prepared, name, raw.Variadic, args, unknown, types)
	if resolution != functions.Resolved {
		return nil, randomSignature(b, raw, args, unknown, resolution == functions.Ambiguous)
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
	usage := b.Type(signature.Result).Usage()
	info := b.Info(usage, raw.Pos, properties)
	node, err := functions.NewMath(info, name, args)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, mathRoutineProvider, name, signature.Args[:signature.Arity]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
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

func bindThresholdBucket(b functions.Binder, raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	operands := make([]functions.Argument, len(raw.Args))
	for i, child := range raw.Args {
		intent := functions.ComparisonArgument
		if i == 1 {
			intent = functions.ThresholdArrayArgument
		}
		operand, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
	}
	names := make([]string, len(operands))
	for i, operand := range operands {
		names[i] = "unknown"
		if operand.Usage.Type != nil {
			names[i] = functions.ArrayTypeName(b, operand.Usage.Type)
		}
	}
	signature := func() (BoundExpr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	if !visible || raw.Variadic || len(raw.ArgNames) != 0 {
		return signature()
	}
	// The threshold array contributes its leaf type to the common type.
	known := make([]sqltype.Usage, 0, len(operands))
	for i, operand := range operands {
		usage := operand.Usage
		if i == 1 && usage.Type != nil {
			leaf, ok := functions.ArraySearchLeafUsage(usage)
			if !ok {
				return signature()
			}
			usage = leaf
		}
		if usage.Type != nil {
			known = append(known, usage)
		}
	}
	common := b.CommonUsage(known)
	array, ok := b.ArrayType(common.Type)
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
	var err error
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
	input, err := b.ComparisonInfo(args[0], args[1])
	if err != nil {
		return nil, err
	}
	result := b.Type(sqltype.Int4).Usage()
	plan, err := sqltype.BindWidthBucketThreshold(result, args[0].ResultUsage(), args[1].ResultUsage(), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, err
	}
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), mathRoutineProvider, "width_bucket", 5077, 5078)
	if err != nil {
		return nil, err
	}
	return functions.Math{Info: info, Plan: plan, Args: args}, nil
}
