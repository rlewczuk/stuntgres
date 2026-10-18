package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindRandom binds random, random_normal and setseed. They sat in the final
// name switch, so the engine applies the shared gates first.
func bindRandom(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if b.DefaultConsumer() {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, unknown, err := AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	prepared := PrepareRandom(name, visible, raw.Variadic, len(args), unknown)
	var types NumericTypes
	if prepared.Needs(sqltype.Int4) {
		types.Int4 = b.Type(sqltype.Int4).Usage()
	}
	if prepared.Needs(sqltype.Int8) {
		types.Int8 = b.Type(sqltype.Int8).Usage()
	}
	if prepared.Needs(sqltype.Float8) {
		types.Float8 = b.Type(sqltype.Float8).Usage()
	}
	if prepared.Needs(sqltype.Numeric) {
		types.Numeric = b.Type(sqltype.Numeric).Usage()
	}
	selected, resolution := SelectRandom(prepared, args, unknown, types)
	if resolution != Resolved {
		return nil, DirectResolutionError(b, raw, args, unknown, resolution == Ambiguous)
	}
	if err := CoerceRandomArguments(b, raw.Args, args, unknown, selected); err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Volatile}
	for _, arg := range args {
		p := arg.Properties()
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	usage := selected.Target
	if usage.Type == nil || usage.Type.ID() != selected.Result {
		usage = b.Type(selected.Result).Usage()
	}
	var formal [2]sqltype.ID
	count := len(args)
	if name == "random_normal" {
		formal, count = [2]sqltype.ID{sqltype.Float8, sqltype.Float8}, 2
	} else {
		for i := range args {
			formal[i] = selected.Target.Type.ID()
		}
	}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), RandomRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if b.LiteralNull(arg) {
			// A strict call with a NULL literal folds to a typed NULL literal.
			return b.Literal(sqltype.Null(usage.Type), raw.Pos), nil
		}
	}
	return Random{Info: info, Name: name, Args: args}, nil
}
