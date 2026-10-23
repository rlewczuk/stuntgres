package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindIntegerIndexSupport(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var oid uint32
	switch name {
	case "btint4cmp":
		oid = 351
	case "hashint4":
		oid = 450
	case "hashint4extended":
		oid = 425
	}
	arity := 2
	if oid == 450 {
		arity = 1
	}
	var operands [2]Argument
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = arg
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != arity || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.CallSignatureError(raw)
	}
	var formals [2]sqltype.ID
	var targets [2]sqltype.Usage
	for i := range arity {
		formals[i] = sqltype.Int4
		if oid == 425 && i == 1 {
			formals[i] = sqltype.Int8
		}
		targets[i] = b.Type(formals[i]).Usage()
	}
	if !knownCallEdges(b, operands[:arity], targets[:arity]) {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	// Applicability is settled; unknown literals and parameters are converted
	// only here so their input errors and inference retain ordinary positions.
	properties := expr.IntrinsicProperties(expr.ExactImmutable)
	for i := range arity {
		args[i], err = b.CoerceArgument(operands[i], CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	result := sqltype.Int4
	if oid == 425 {
		result = sqltype.Int8
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), IndexSupportRoutineProvider, name, formals[:arity]...)
	if err != nil {
		return nil, err
	}
	return IntegerIndexSupport{Info: info, OID: oid, Args: args}, nil
}
