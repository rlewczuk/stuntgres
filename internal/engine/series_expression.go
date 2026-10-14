package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A series is a retained typed target marker, never an executable generator.
// Invocation-private arithmetic lives in the shared functionSetCursor.
type seriesExpression struct {
	expressionInfo
	args []BoundExpr
}

func (e seriesExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_series set call")
}

func (e seriesExpression) mapChildren(mapper func(BoundExpr) (BoundExpr, bool, error)) (seriesExpression, bool, error) {
	changed := false
	for i, arg := range e.args {
		next, did, err := mapper(arg)
		if err != nil {
			return e, false, err
		}
		if did {
			if !changed {
				e.args = append([]BoundExpr(nil), e.args...)
			}
			e.args[i] = next
			changed = true
		}
	}
	return e, changed, nil
}

type seriesSignature struct {
	args   [4]sqltype.ID
	arity  int
	result sqltype.ID
}

// Include released nonselected signatures: execution scope cannot alter overload
// ambiguity, implicit input selection, or genuine missing-signature diagnostics.
var seriesSignatures = [...]seriesSignature{
	{[4]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4},
	{[4]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8},
	{[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric},
	{[4]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int4}, 3, sqltype.Int4},
	{[4]sqltype.ID{sqltype.Int8, sqltype.Int8, sqltype.Int8}, 3, sqltype.Int8},
	{[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric, sqltype.Numeric}, 3, sqltype.Numeric},
	{[4]sqltype.ID{sqltype.Timestamp, sqltype.Timestamp, sqltype.Interval}, 3, sqltype.Timestamp},
	{[4]sqltype.ID{sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Interval}, 3, sqltype.Timestamptz},
	{[4]sqltype.ID{sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 4, sqltype.Timestamptz},
}

// Released candidate preferred flags include datetime and interval; the
// common-value preferredType helper does not cover those function categories.
func seriesPreferred(id sqltype.ID) bool {
	return id == sqltype.Timestamptz || id == sqltype.Interval || id == sqltype.Text
}

func (b *expressionBinder) bindSeries(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "generate_series" || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.seriesSignatureError(raw, args, unknown, false)
	}
	// Fixed bounded candidate storage; only the retained argument tree allocates.
	var storage [len(seriesSignatures)]seriesSignature
	candidates := storage[:0]
	bestExact, bestPreferred := -1, -1
	for _, s := range seriesSignatures {
		if s.arity != len(args) {
			continue
		}
		viable, exact, preferred := true, 0, 0
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			input := arg.ResultUsage()
			target := b.typ(s.args[i])
			id := input.Type.BaseType().ID()
			if _, err := sqltype.PlanCoercion(input, target.Usage(), sqltype.Implicit); err != nil {
				viable = false
				break
			}
			if id == s.args[i] {
				exact++
			} else if seriesPreferred(s.args[i]) && typeCategory(input.Type) == typeCategory(target) {
				preferred++
			}
		}
		if !viable {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			candidates = candidates[:0]
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			candidates = append(candidates, s)
		}
	}
	for i, u := range unknown {
		if !u || len(candidates) < 2 {
			continue
		}
		category := typeCategory(b.typ(candidates[0].args[i]))
		consistent, hasString := true, false
		for _, s := range candidates {
			cat := typeCategory(b.typ(s.args[i]))
			consistent = consistent && cat == category
			hasString = hasString || cat == 'S'
		}
		if hasString {
			category = 'S'
		} else if !consistent {
			continue
		}
		preferred := false
		for _, s := range candidates {
			preferred = preferred || typeCategory(b.typ(s.args[i])) == category && seriesPreferred(s.args[i])
		}
		filtered := candidates[:0]
		for _, s := range candidates {
			if typeCategory(b.typ(s.args[i])) == category && (!preferred || seriesPreferred(s.args[i])) {
				filtered = append(filtered, s)
			}
		}
		candidates = filtered
	}
	if len(candidates) > 1 {
		var known sqltype.ID
		homogeneous := true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.BaseType().ID()
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
		return nil, b.seriesSignatureError(raw, args, unknown, len(candidates) > 1)
	}
	s := candidates[0]
	properties := ExprProperties{Volatility: Immutable, StageDependent: true}
	for i, arg := range args {
		u := b.typ(s.args[i]).Usage()
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
	}
	decoration, message := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, message = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		return nil, expressionError("42809", decoration+" specified, but generate_series is "+message, raw.Pos)
	}
	if b.executeParameters {
		return nil, expressionError("0A000", "set-returning functions are not allowed in EXECUTE parameters", raw.Pos)
	}
	if b.policyConsumer && b.queryFrame == 0 {
		return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
	}
	if b.aggregateClause == "INSERT VALUES" {
		return nil, expressionError("0A000", "generate_series is unsupported in INSERT VALUES", raw.Pos)
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	if s.result != sqltype.Int4 && s.result != sqltype.Int8 {
		kind := "temporal"
		if s.result == sqltype.Numeric {
			kind = "numeric"
		}
		return nil, expressionError("0A000", "generate_series with "+kind+" arguments is unsupported", raw.Pos)
	}
	switch b.aggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.queryHasTargetSet = true
	}
	return seriesExpression{expressionInfo: b.info(b.typ(s.result).Usage(), raw.Pos, properties), args: args}, nil
}

func (b *expressionBinder) seriesSignatureError(raw parser.FunctionExpr, args []BoundExpr, unknown []bool, ambiguous bool) error {
	names := make([]string, len(args))
	for i, arg := range args {
		name := "unknown"
		if !unknown[i] && arg.ResultUsage().Type != nil {
			name = b.diagnosticTypeName(arg.ResultUsage().Type)
		}
		names[i] = functionNamedType(raw, i, name)
	}
	return b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
}
