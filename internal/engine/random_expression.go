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

func (b *expressionBinder) randomSignature(raw parser.FunctionExpr, args []BoundExpr, unknown []bool, ambiguous bool) error {
	types := make([]string, len(args))
	for i, arg := range args {
		types[i] = "unknown"
		if !unknown[i] && arg.ResultUsage().Type != nil {
			types[i] = arg.ResultUsage().Type.DiagnosticName()
		}
	}
	return b.functionResolutionErrorForTypes(raw.Name, raw.Pos, types, ambiguous)
}

func (b *expressionBinder) bindRandom(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, unknown, err := functions.AnalyzeDirectArguments(b, raw.Args)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	prepared := functions.PrepareRandom(name, visible, raw.Variadic, len(args), unknown)
	var types functions.NumericTypes
	if prepared.Needs(sqltype.Int4) {
		types.Int4 = b.typ(sqltype.Int4).Usage()
	}
	if prepared.Needs(sqltype.Int8) {
		types.Int8 = b.typ(sqltype.Int8).Usage()
	}
	if prepared.Needs(sqltype.Float8) {
		types.Float8 = b.typ(sqltype.Float8).Usage()
	}
	if prepared.Needs(sqltype.Numeric) {
		types.Numeric = b.typ(sqltype.Numeric).Usage()
	}
	selected, resolution := functions.SelectRandom(prepared, args, unknown, types)
	if resolution != functions.Resolved {
		return nil, b.randomSignature(raw, args, unknown, resolution == functions.Ambiguous)
	}
	if err := functions.CoerceRandomArguments(b, raw.Args, args, unknown, selected); err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: Volatile}
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
		usage = b.typ(selected.Result).Usage()
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
	info, err := b.builtinRoutineInfo(b.info(usage, raw.Pos, properties), randomRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if randomLiteralNull(arg) {
			return literalExpression{b.info(usage, raw.Pos, ExprProperties{}), sqltype.Null(usage.Type)}, nil
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
