package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionRandomOwner() (expr.RandomOwner, error) {
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return &c.liveScope().op.session.random, nil
}

func bindRandom(b functions.Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	if b.DefaultConsumer() {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, unknown, err := functions.AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	prepared := functions.PrepareRandom(name, visible, raw.Variadic, len(args), unknown)
	var types functions.NumericTypes
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
	selected, resolution := functions.SelectRandom(prepared, args, unknown, types)
	if resolution != functions.Resolved {
		return nil, functions.DirectResolutionError(b, raw, args, unknown, resolution == functions.Ambiguous)
	}
	if err := functions.CoerceRandomArguments(b, raw.Args, args, unknown, selected); err != nil {
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
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), functions.RandomRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if b.LiteralNull(arg) {
			// A strict call with a NULL literal folds to a typed NULL literal.
			return b.Literal(sqltype.Null(usage.Type), raw.Pos), nil
		}
	}
	return functions.Random{Info: info, Name: name, Args: args}, nil
}

func randomLiteralNull(arg BoundExpr) bool {
	switch e := arg.(type) {
	case literalExpression:
		return e.value.IsNull()
	case collationExpression:
		return randomLiteralNull(e.child)
	case coercionExpression:
		return randomLiteralNull(e.child)
	}
	return false
}
