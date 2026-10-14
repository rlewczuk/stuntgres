package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindMath(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
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
	if err := functions.CoerceMathArguments(b, raw.Args, args, unknown, signature, types); err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: Immutable}
	for _, arg := range args {
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	usage := b.typ(signature.Result).Usage()
	return functions.NewMath[BoundExpr, *EvalContext](functionInfo(b.info(usage, raw.Pos, properties)), name, args)
}
