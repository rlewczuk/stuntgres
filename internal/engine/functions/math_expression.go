package functions

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Math[E Expression[C], C LiteralContext[E], P any] struct {
	Info[P]
	Plan sqltype.MathPlan
	Args []E
}

func (e Math[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.Plan.Evaluate(ctx, values[0], values[1])
}
func (e Math[E, C, P]) Reconstruct(info Info[P], args []E) (Math[E, C, P], error) {
	if len(args) > 2 {
		return e, fmt.Errorf("stuntgres: invalid remapped math arity")
	}
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindMath(e.Plan.Name(), info.Usage, usages[:len(args)]...)
	if err != nil {
		return e, err
	}
	return Math[E, C, P]{info, plan, args}, nil
}
func (e Math[E, C, P]) Validate() error {
	if len(e.Args) > 2 {
		return fmt.Errorf("stuntgres: invalid stored math arity")
	}
	var usages [2]sqltype.Usage
	for i, arg := range e.Args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindMath(e.Plan.Name(), e.Usage, usages[:len(e.Args)]...)
	if err != nil || plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored math provider")
	}
	return nil
}

type NumericTypes struct{ Int2, Int4, Int8, Float4, Float8, Numeric sqltype.Usage }

func (t NumericTypes) usage(id sqltype.ID) sqltype.Usage {
	switch id {
	case sqltype.Int2:
		return t.Int2
	case sqltype.Int4:
		return t.Int4
	case sqltype.Int8:
		return t.Int8
	case sqltype.Float4:
		return t.Float4
	case sqltype.Float8:
		return t.Float8
	case sqltype.Numeric:
		return t.Numeric
	}
	return sqltype.Usage{}
}

type MathSignature struct {
	Args   [2]sqltype.ID
	Arity  int
	Result sqltype.ID
}

func mathSignatures(name string) []MathSignature {
	unary := func(id sqltype.ID) MathSignature { return MathSignature{[2]sqltype.ID{id}, 1, id} }
	switch name {
	case "pi":
		return []MathSignature{{Arity: 0, Result: sqltype.Float8}}
	case "scale", "min_scale":
		return []MathSignature{{[2]sqltype.ID{sqltype.Numeric}, 1, sqltype.Int4}}
	case "trim_scale":
		return []MathSignature{unary(sqltype.Numeric)}
	case "abs":
		return []MathSignature{unary(sqltype.Int2), unary(sqltype.Int4), unary(sqltype.Int8), unary(sqltype.Float4), unary(sqltype.Float8), unary(sqltype.Numeric)}
	case "div":
		return []MathSignature{{[2]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	case "gcd", "lcm":
		return []MathSignature{{[2]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4}, {[2]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8}, {[2]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	case "factorial":
		return []MathSignature{{[2]sqltype.ID{sqltype.Int8}, 1, sqltype.Numeric}}
	case "sign", "ceil", "ceiling", "floor", "exp":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8)}
	case "cbrt", "degrees", "radians", "erf", "erfc", "gamma", "acos", "acosd", "asin", "asind", "atan", "atand", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand", "sinh", "cosh", "tanh", "asinh", "acosh", "atanh":
		return []MathSignature{unary(sqltype.Float8)}
	case "atan2", "atan2d":
		return []MathSignature{{[2]sqltype.ID{sqltype.Float8, sqltype.Float8}, 2, sqltype.Float8}}
	case "round", "trunc":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8), {[2]sqltype.ID{sqltype.Numeric, sqltype.Int4}, 2, sqltype.Numeric}}
	case "mod":
		return []MathSignature{{[2]sqltype.ID{sqltype.Int2, sqltype.Int2}, 2, sqltype.Int2}, {[2]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4}, {[2]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8}, {[2]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	}
	return nil
}

type MathCandidates struct {
	signatures []MathSignature
	demand     uint8
}

func numericTypeBit(id sqltype.ID) uint8 {
	switch id {
	case sqltype.Int2:
		return 1
	case sqltype.Int4:
		return 2
	case sqltype.Int8:
		return 4
	case sqltype.Float4:
		return 8
	case sqltype.Float8:
		return 16
	case sqltype.Numeric:
		return 32
	}
	return 0
}
func PrepareMath(name string) MathCandidates {
	prepared := MathCandidates{signatures: mathSignatures(name)}
	for _, signature := range prepared.signatures {
		for _, id := range signature.Args[:signature.Arity] {
			prepared.demand |= numericTypeBit(id)
		}
	}
	return prepared
}
func (p MathCandidates) Needs(id sqltype.ID) bool { return p.demand&numericTypeBit(id) != 0 }
func SelectMath[E interface{ ResultUsage() sqltype.Usage }](prepared MathCandidates, name string, variadic bool, args []E, unknown []bool, types NumericTypes) (MathSignature, Resolution) {
	if name == "trunc" && len(args) == 1 && unknown[0] && !variadic {
		return MathSignature{}, Ambiguous
	}
	var candidates []MathSignature
	bestExact, bestPreferred := -1, -1
	for _, s := range prepared.signatures {
		if variadic || s.Arity != len(args) {
			continue
		}
		viable, exact, preferred := true, 0, 0
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.BaseType().ID()
			if _, err := sqltype.PlanCoercion(arg.ResultUsage(), types.usage(s.Args[i]), sqltype.Implicit); err != nil {
				viable = false
				break
			}
			if id == s.Args[i] {
				exact++
			} else if s.Args[i] == sqltype.Float8 {
				preferred++
			}
		}
		if !viable {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			candidates = nil
			bestExact = exact
			bestPreferred = preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			candidates = append(candidates, s)
		}
	}
	// FLOAT8 is the numeric category's preferred type, including unknown literals.
	for i, isUnknown := range unknown {
		if !isUnknown || len(candidates) < 2 {
			continue
		}
		preferred := false
		for _, s := range candidates {
			preferred = preferred || s.Args[i] == sqltype.Float8
		}
		if preferred {
			filtered := candidates[:0]
			for _, s := range candidates {
				if s.Args[i] == sqltype.Float8 {
					filtered = append(filtered, s)
				}
			}
			candidates = filtered
		}
	}
	if len(candidates) > 1 {
		var known sqltype.Usage
		homogeneous := true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.BaseType().ID()
			if known.Type != nil && known.Type.ID() != id {
				homogeneous = false
			}
			known = arg.ResultUsage().Type.BaseType().Usage()
		}
		if homogeneous && known.Type != nil {
			filtered := candidates[:0]
			for _, s := range candidates {
				viable := true
				for i, u := range unknown {
					if u {
						if _, err := sqltype.PlanCoercion(known, types.usage(s.Args[i]), sqltype.Implicit); err != nil {
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
		if len(candidates) > 1 {
			return MathSignature{}, Ambiguous
		}
		return MathSignature{}, Missing
	}
	return candidates[0], Resolved
}

func CoerceMathArguments[E interface{ ResultUsage() sqltype.Usage }, B ArgumentCoercer[E]](b B, raw []parser.Expr, args []E, unknown []bool, signature MathSignature, types NumericTypes) error {
	for i, arg := range args {
		argument := Argument[E]{Expression: arg, Raw: raw[i]}
		if !unknown[i] {
			argument.Usage = arg.ResultUsage()
		}
		node, err := b.CoerceArgument(argument, CoercionRequest{Intent: DirectFormalArgument, Target: types.usage(signature.Args[i]), PreserveProperties: true, AtRaw: true})
		if err != nil {
			return err
		}
		args[i] = node
	}
	return nil
}
func NewMath[E Expression[C], C LiteralContext[E], P any](info Info[P], name string, args []E) (Math[E, C, P], error) {
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindMath(name, info.Usage, usages[:len(args)]...)
	return Math[E, C, P]{info, plan, args}, err
}
