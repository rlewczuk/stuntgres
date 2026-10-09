package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type randomExpression struct {
	expressionInfo
	name string
	args []BoundExpr
}

func (x randomExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range x.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for i := range x.args {
		if values[i].IsNull() {
			return sqltype.Null(x.usage.Type), nil
		}
	}
	if c.statementScope == nil || c.statementScope.op == nil || c.statementScope.op.session == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing random session scope")
	}
	s := &c.statementScope.op.session.random
	if x.name == "setseed" {
		seed, err := values[0].Float()
		if err != nil {
			return sqltype.Value{}, err
		}
		if math.IsNaN(seed) || seed < -1 || seed > 1 {
			text := strconv.FormatFloat(seed, 'g', 6, 64)
			if math.IsInf(seed, 1) {
				text = "Infinity"
			}
			if math.IsInf(seed, -1) {
				text = "-Infinity"
			}
			return sqltype.Value{}, sqlError("22023", "setseed parameter "+text+" is out of allowed range [-1,1]")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reseed(seed)
		return sqltype.VoidValue(x.usage.Type), nil
	}
	var lower, upper int64
	if x.name == "random" && len(x.args) == 2 && x.usage.Type.ID() != sqltype.Numeric {
		var err error
		lower, err = values[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		upper, err = values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if lower > upper {
			return sqltype.Value{}, sqlError("22023", "lower bound must be less than or equal to upper bound")
		}
		if lower == upper {
			return sqltype.Integer(x.usage.Type, lower)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initialize(); err != nil {
		return sqltype.Value{}, err
	}
	switch x.name {
	case "random":
		if len(x.args) == 0 {
			return sqltype.Float(x.usage.Type, s.rand.Float64())
		}
		if x.usage.Type.ID() == sqltype.Numeric {
			return sqltype.NumericRandom(c.Context, values[0], values[1], s.rand.Uint64)
		}
		return sqltype.Integer(x.usage.Type, randomInteger(lower, upper, s.rand.Uint64))
	case "random_normal":
		mean, deviation := float64(0), float64(1)
		var err error
		if len(x.args) > 0 {
			mean, err = values[0].Float()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if len(x.args) > 1 {
			deviation, err = values[1].Float()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		return sqltype.Float(x.usage.Type, deviation*s.rand.NormFloat64()+mean)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid random expression")
}

func (b *expressionBinder) randomSignature(raw parser.FunctionExpr, args []BoundExpr, unknown []bool, ambiguous bool) error {
	types := make([]string, len(args))
	for i, arg := range args {
		types[i] = "unknown"
		if !unknown[i] && arg.ResultUsage().Type != nil {
			types[i] = arg.ResultUsage().Type.DiagnosticName()
		}
	}
	code, ending, hint := "42883", "does not exist", functionCastHint
	if ambiguous {
		code, ending, hint = "42725", "is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	d := expressionError(code, fmt.Sprintf("function %s(%s) %s", objectNameText(raw.Name), strings.Join(types, ", "), ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *expressionBinder) bindRandom(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
			// Leave unknown literal input uninterpreted until overload selection.
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
	// PostgreSQL's general function-call limit precedes overload lookup, but
	// only after every child has been analyzed for its own errors/captures.
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	id := sqltype.Float8
	valid := visible && !raw.Variadic
	switch name {
	case "setseed":
		valid = valid && len(args) == 1
		id = sqltype.Void
	case "random_normal":
		valid = valid && len(args) <= 2
	case "random":
		valid = valid && (len(args) == 0 || len(args) == 2)
	}
	if !valid {
		return nil, b.randomSignature(raw, args, unknown, false)
	}
	argumentID := sqltype.Float8
	if name == "random" && len(args) == 2 {
		bestScore := -1
		var candidates []sqltype.ID
		for _, candidate := range []sqltype.ID{sqltype.Int4, sqltype.Int8, sqltype.Numeric} {
			viable, score := true, 0
			for i, arg := range args {
				if unknown[i] {
					continue
				}
				if arg.ResultUsage().Type.BaseType().ID() == candidate {
					score++
				}
				if _, err := sqltype.PlanCoercion(arg.ResultUsage(), b.typ(candidate).Usage(), sqltype.Implicit); err != nil {
					viable = false
					break
				}
			}
			if !viable {
				continue
			}
			if score > bestScore {
				bestScore = score
				candidates = candidates[:0]
			}
			if score == bestScore {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) == 0 {
			return nil, b.randomSignature(raw, args, unknown, false)
		}
		if len(candidates) != 1 {
			return nil, b.randomSignature(raw, args, unknown, true)
		}
		id = candidates[0]
		argumentID = id
	}
	// Resolve the whole signature before coercing any child, preserving the
	// original argument types in missing-signature diagnostics.
	var coercions [2]sqltype.CoercionPlan
	for i, arg := range args {
		if unknown[i] {
			continue
		}
		var err error
		coercions[i], err = sqltype.PlanCoercion(arg.ResultUsage(), b.typ(argumentID).Usage(), sqltype.Implicit)
		if err != nil {
			return nil, b.randomSignature(raw, args, unknown, false)
		}
	}
	for i, arg := range args {
		u := b.typ(argumentID).Usage()
		if unknown[i] {
			var err error
			args[i], err = b.bind(raw.Args[i], expressionPolicy{context: functionArgumentContext, expected: u, mode: sqltype.Implicit})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != u {
			args[i] = coercionExpression{b.info(u, raw.Args[i].SourcePosition(), arg.Properties()), arg, coercions[i], sqltype.Implicit}
		}
	}
	properties := ExprProperties{Volatility: Volatile}
	for _, arg := range args {
		p := arg.Properties()
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	usage := b.typ(id).Usage()
	for _, arg := range args {
		if randomLiteralNull(arg) {
			return literalExpression{b.info(usage, raw.Pos, ExprProperties{}), sqltype.Null(usage.Type)}, nil
		}
	}
	return randomExpression{b.info(usage, raw.Pos, properties), name, args}, nil
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
