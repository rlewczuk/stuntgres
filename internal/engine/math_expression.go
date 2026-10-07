package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type mathExpression struct {
	expressionInfo
	plan sqltype.MathPlan
	args []BoundExpr
}

func (e mathExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.args {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Evaluate(c.Context, values[0], values[1])
}

type mathSignature struct {
	args   [2]sqltype.ID
	arity  int
	result sqltype.ID
}

func mathSignatures(name string) []mathSignature {
	unary := func(id sqltype.ID) mathSignature { return mathSignature{[2]sqltype.ID{id}, 1, id} }
	switch name {
	case "scale", "min_scale":
		return []mathSignature{{[2]sqltype.ID{sqltype.Numeric}, 1, sqltype.Int4}}
	case "trim_scale":
		return []mathSignature{unary(sqltype.Numeric)}
	case "sign":
		return []mathSignature{unary(sqltype.Numeric), unary(sqltype.Float8)}
	case "round", "trunc":
		return []mathSignature{unary(sqltype.Numeric), unary(sqltype.Float8), {[2]sqltype.ID{sqltype.Numeric, sqltype.Int4}, 2, sqltype.Numeric}}
	case "mod":
		return []mathSignature{
			{[2]sqltype.ID{sqltype.Int2, sqltype.Int2}, 2, sqltype.Int2},
			{[2]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4},
			{[2]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8},
			{[2]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric},
		}
	}
	return nil
}

func (b *expressionBinder) bindMath(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var args []BoundExpr
	var unknown []bool
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
		unknown = make([]bool, len(raw.Args))
	}
	for i, r := range raw.Args {
		if l, ok := r.(parser.LiteralExpr); ok && (l.Value.Kind == parser.StringValue || l.Value.Kind == parser.NullValue) {
			if args != nil {
				unknown[i] = true
				args[i] = literalExpression{expressionInfo: expressionInfo{pos: r.SourcePosition()}}
			}
			continue
		}
		x, err := b.bind(r, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = x
			unknown[i] = x.ResultUsage().Type == nil
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if !visible {
		return nil, b.randomSignature(raw, args, unknown, false)
	}
	// Released trunc also has nonnumeric overloads in other categories. An
	// unknown unary argument cannot select the bounded numeric repertoire.
	if name == "trunc" && len(args) == 1 && unknown[0] && !raw.Variadic {
		return nil, b.randomSignature(raw, args, unknown, true)
	}
	var candidates []mathSignature
	bestExact, bestPreferred := -1, -1
	for _, s := range mathSignatures(name) {
		if raw.Variadic || s.arity != len(args) {
			continue
		}
		viable, exact, preferred := true, 0, 0
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.ID()
			if _, err := sqltype.PlanCoercion(arg.ResultUsage(), b.typ(s.args[i]).Usage(), sqltype.Implicit); err != nil {
				viable = false
				break
			}
			if id == s.args[i] {
				exact++
			} else if s.args[i] == sqltype.Float8 {
				preferred++
			}
		}
		if !viable {
			continue
		}
		if exact > bestExact || (exact == bestExact && preferred > bestPreferred) {
			candidates = nil
			bestExact = exact
			bestPreferred = preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			candidates = append(candidates, s)
		}
	}
	// Every selected overload is numeric-category. FLOAT8 is that category's
	// preferred type, including when the argument was an unknown string/NULL.
	for i, isUnknown := range unknown {
		if !isUnknown || len(candidates) < 2 {
			continue
		}
		preferred := false
		for _, s := range candidates {
			preferred = preferred || s.args[i] == sqltype.Float8
		}
		if preferred {
			filtered := candidates[:0]
			for _, s := range candidates {
				if s.args[i] == sqltype.Float8 {
					filtered = append(filtered, s)
				}
			}
			candidates = filtered
		}
	}
	if len(candidates) > 1 {
		var known sqltype.ID
		homogeneous := true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.ID()
			if known != 0 && known != id {
				homogeneous = false
			}
			known = id
		}
		if homogeneous && known != 0 {
			filtered := candidates[:0]
			for _, s := range candidates {
				viable := true
				for i, u := range unknown {
					if u {
						if _, err := sqltype.PlanCoercion(b.typ(known).Usage(), b.typ(s.args[i]).Usage(), sqltype.Implicit); err != nil {
							viable = false
						}
					}
				}
				if viable {
					filtered = append(filtered, s)
				}
			}
			candidates = filtered
		}
	}
	if len(candidates) != 1 {
		return nil, b.randomSignature(raw, args, unknown, len(candidates) > 1)
	}
	s := candidates[0]
	var usages [2]sqltype.Usage
	properties := ExprProperties{Volatility: Immutable}
	for i, arg := range args {
		u := b.typ(s.args[i]).Usage()
		usages[i] = u
		if unknown[i] {
			var err error
			args[i], err = b.bind(raw.Args[i], expressionPolicy{context: functionArgumentContext, expected: u, mode: sqltype.Implicit})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != u {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), u, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			args[i] = coercionExpression{b.info(u, raw.Args[i].SourcePosition(), arg.Properties()), arg, plan, sqltype.Implicit}
		}
		p := args[i].Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	u := b.typ(s.result).Usage()
	plan, err := sqltype.BindMath(name, u, usages[:s.arity]...)
	if err != nil {
		return nil, err
	}
	return mathExpression{b.info(u, raw.Pos, properties), plan, args}, nil
}
