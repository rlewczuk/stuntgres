package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// mathNames are the scalar math functions dispatched by exact spelling.
var mathNames = [...]string{
	"lgamma", "ln", "log", "log10", "power", "sqrt", "width_bucket",
	"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
	"cbrt", "degrees", "radians", "pi", "erf", "erfc", "gamma", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod", "abs", "ceil", "ceiling", "div", "exp", "factorial", "floor", "gcd", "lcm",
	"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand",
}

// bindMath runs after the engine's shared call gates (SharedGates); builtin
// visibility follows them, as it did in the engine's final name switch.
func bindMath(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.DefaultConsumer() && !selectedLogPower(name) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if name == "width_bucket" && len(raw.Args) == 2 {
		return bindThresholdBucket(b, raw, visible)
	}
	args, unknown, err := AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if !visible {
		return nil, DirectResolutionError(b, raw, args, unknown, false)
	}
	prepared := PrepareMath(name)
	var types NumericTypes
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
	signature, resolution := SelectMath(prepared, name, raw.Variadic, args, unknown, types)
	if resolution != Resolved {
		return nil, DirectResolutionError(b, raw, args, unknown, resolution == Ambiguous)
	}
	if selectedLogPower(name) {
		if err := mathModifiers(raw, name); err != nil {
			return nil, err
		}
	}
	if err := CoerceMathArguments(b, name, raw.Args, args, unknown, signature, types); err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	usage := b.Type(signature.Result).Usage()
	info := b.Info(usage, raw.Pos, properties)
	node, err := NewMath(info, name, args)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, MathRoutineProvider, name, signature.Args[:signature.Arity]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	node.WrapperContext = mathWrapperContext(name, args)
	return node, nil
}

// mathWrapperContext is the PostgreSQL SQL-function wrapper context that
// numeric log/log10 report for a non-row-dependent argument; retag recomputes
// it after rebuilding a call.
func mathWrapperContext(name string, args []expr.Expr) string {
	if (name == "log" || name == "log10") && len(args) == 1 && args[0].ResultUsage().Type.BaseType().ID() == sqltype.Numeric && !args[0].Properties().RowDependent {
		return "SQL function \"" + name + "\" statement 1"
	}
	return ""
}

func mathModifiers(raw parser.FunctionExpr, name string) error {
	switch {
	case raw.Star:
		return expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return expr.ErrorAt("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	return nil
}

func bindThresholdBucket(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		intent := ComparisonArgument
		if i == 1 {
			intent = ThresholdArrayArgument
		}
		operand, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
	}
	names := make([]string, len(operands))
	for i, operand := range operands {
		names[i] = "unknown"
		if operand.Usage.Type != nil {
			names[i] = ArrayTypeName(b, operand.Usage.Type)
		}
	}
	signature := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	if !visible || raw.Variadic || len(raw.ArgNames) != 0 {
		return signature()
	}
	arrays := [2]bool{false, true}
	var targets [2]sqltype.Usage
	if _, ok := compatibleArrayTargets(b, operands, arrays[:], targets[:], false); !ok {
		return signature()
	}
	if err := mathModifiers(raw, "width_bucket"); err != nil {
		return nil, err
	}
	args := make([]expr.Expr, 2)
	properties := expr.Properties{Volatility: expr.Immutable}
	var err error
	for i, operand := range operands {
		intent := DirectFormalArgument
		if _, parameter := operand.Raw.(parser.ParameterExpr); parameter && operand.Usage.Type == nil {
			// Preserve occurrence-sensitive inference for scalar/array formals,
			// just as for the selected fixed-signature math overloads.
			intent = FinalizeArgument
		}
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: intent, Target: targets[i], PreserveProperties: true, AtRaw: true})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
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
		properties.Volatility = max(properties.Volatility, expr.Stable)
	}
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), MathRoutineProvider, "width_bucket", 5077, 5078)
	if err != nil {
		return nil, err
	}
	return Math{Info: info, Plan: plan, Args: args}, nil
}
