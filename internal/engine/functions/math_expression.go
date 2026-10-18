package functions

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Math struct {
	expr.Info
	Plan           sqltype.MathPlan
	Args           []expr.Expr
	WrapperContext string
}

func (e Math) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !selectedLogPower(e.Plan.Name()) {
		for _, arg := range e.Args {
			if c.FunctionLiteralNull(arg) {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
	}
	var values [4]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	value, err := e.Plan.Evaluate(ctx, values[:len(e.Args)], c.FunctionInput())
	if err != nil && e.WrapperContext != "" {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Context = e.WrapperContext
			err = &copy
		}
	}
	return value, err
}
func (e Math) Reconstruct(info expr.Info, args []expr.Expr, ref sqltype.CollationRef, conflict bool) (Math, error) {
	if len(args) > 4 {
		return e, fmt.Errorf("stuntgres: invalid remapped math arity")
	}
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := bindMathProvider(e.Plan.Name(), info.Usage, usages[:len(args)], ref, conflict)
	if err != nil {
		return e, err
	}
	e.Info, e.Plan, e.Args = info, plan, args
	return e, nil
}
func (e Math) Validate() error {
	if len(e.Args) > 4 {
		return fmt.Errorf("stuntgres: invalid stored math arity")
	}
	var usages [4]sqltype.Usage
	for i, arg := range e.Args {
		usages[i] = arg.ResultUsage()
	}
	ref, conflict := e.Plan.InputCollation()
	plan, err := bindMathProvider(e.Plan.Name(), e.Usage, usages[:len(e.Args)], ref, conflict)
	if err != nil || !plan.Equal(e.Plan) {
		return fmt.Errorf("stuntgres: invalid stored math provider")
	}
	return nil
}

func bindMathProvider(name string, result sqltype.Usage, args []sqltype.Usage, ref sqltype.CollationRef, conflict bool) (sqltype.MathPlan, error) {
	if name == "width_bucket" && len(args) == 2 {
		return sqltype.BindWidthBucketThreshold(result, args[0], args[1], ref, conflict)
	}
	return sqltype.BindMath(name, result, args...)
}

// selectedLogPower reports the math names admitted in column DEFAULT
// expressions; they also keep strict literal-NULL folding out of the
// conditional planner.
func selectedLogPower(name string) bool {
	switch name {
	case "lgamma", "ln", "log", "log10", "power", "sqrt", "width_bucket":
		return true
	}
	return false
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
	Args   [4]sqltype.ID
	Arity  int
	Result sqltype.ID
}

func mathSignatures(name string) []MathSignature {
	unary := func(id sqltype.ID) MathSignature { return MathSignature{[4]sqltype.ID{id}, 1, id} }
	switch name {
	case "pi":
		return []MathSignature{{Arity: 0, Result: sqltype.Float8}}
	case "scale", "min_scale":
		return []MathSignature{{[4]sqltype.ID{sqltype.Numeric}, 1, sqltype.Int4}}
	case "trim_scale":
		return []MathSignature{unary(sqltype.Numeric)}
	case "abs":
		return []MathSignature{unary(sqltype.Int2), unary(sqltype.Int4), unary(sqltype.Int8), unary(sqltype.Float4), unary(sqltype.Float8), unary(sqltype.Numeric)}
	case "div":
		return []MathSignature{{[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	case "gcd", "lcm":
		return []MathSignature{{[4]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4}, {[4]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8}, {[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	case "factorial":
		return []MathSignature{{[4]sqltype.ID{sqltype.Int8}, 1, sqltype.Numeric}}
	case "ln", "log10", "sqrt":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8)}
	case "lgamma":
		return []MathSignature{unary(sqltype.Float8)}
	case "log":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8), {[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
	case "power":
		return []MathSignature{{[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}, {[4]sqltype.ID{sqltype.Float8, sqltype.Float8}, 2, sqltype.Float8}}
	case "width_bucket":
		return []MathSignature{{[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric, sqltype.Numeric, sqltype.Int4}, 4, sqltype.Int4}, {[4]sqltype.ID{sqltype.Float8, sqltype.Float8, sqltype.Float8, sqltype.Int4}, 4, sqltype.Int4}}
	case "sign", "ceil", "ceiling", "floor", "exp":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8)}
	case "cbrt", "degrees", "radians", "erf", "erfc", "gamma", "acos", "acosd", "asin", "asind", "atan", "atand", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand", "sinh", "cosh", "tanh", "asinh", "acosh", "atanh":
		return []MathSignature{unary(sqltype.Float8)}
	case "atan2", "atan2d":
		return []MathSignature{{[4]sqltype.ID{sqltype.Float8, sqltype.Float8}, 2, sqltype.Float8}}
	case "round", "trunc":
		return []MathSignature{unary(sqltype.Numeric), unary(sqltype.Float8), {[4]sqltype.ID{sqltype.Numeric, sqltype.Int4}, 2, sqltype.Numeric}}
	case "mod":
		return []MathSignature{{[4]sqltype.ID{sqltype.Int2, sqltype.Int2}, 2, sqltype.Int2}, {[4]sqltype.ID{sqltype.Int4, sqltype.Int4}, 2, sqltype.Int4}, {[4]sqltype.ID{sqltype.Int8, sqltype.Int8}, 2, sqltype.Int8}, {[4]sqltype.ID{sqltype.Numeric, sqltype.Numeric}, 2, sqltype.Numeric}}
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
func SelectMath(prepared MathCandidates, name string, variadic bool, args []expr.Expr, unknown []bool, types NumericTypes) (MathSignature, Resolution) {
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

func CoerceMathArguments(b ArgumentCoercer, name string, raw []parser.Expr, args []expr.Expr, unknown []bool, signature MathSignature, types NumericTypes) error {
	for i, arg := range args {
		argument := Argument{Expression: arg, Raw: raw[i]}
		if !unknown[i] {
			argument.Usage = arg.ResultUsage()
		}
		intent := DirectFormalArgument
		if _, parameter := raw[i].(parser.ParameterExpr); parameter && unknown[i] && selectedLogPower(name) {
			// Occurrence-sensitive inference uses the existing parameter owner.
			// Literals and already typed arguments keep ordinary formal coercion.
			intent = FinalizeArgument
		}
		node, err := b.CoerceArgument(argument, CoercionRequest{Intent: intent, Target: types.usage(signature.Args[i]), PreserveProperties: true, AtRaw: true})
		if err != nil {
			return err
		}
		args[i] = node
	}
	return nil
}
func NewMath(info expr.Info, name string, args []expr.Expr) (Math, error) {
	if len(args) > 4 {
		return Math{}, fmt.Errorf("invalid math arity")
	}
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindMath(name, info.Usage, usages[:len(args)]...)
	return Math{Info: info, Plan: plan, Args: args}, err
}

// Math implements expr.Composite. Rebuild keeps the bound comparison collation
// and the wrapper context chosen at binding.
var _ expr.Composite = Math{}

func (e Math) Children() []expr.Expr { return e.Args }
func (e Math) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Math", children, len(e.Args)); err != nil {
		return nil, err
	}
	if err := checkPresent("stuntgres: invalid remapped math argument", children); err != nil {
		return nil, err
	}
	ref, conflict := e.Plan.InputCollation()
	return rebuilt(e.Reconstruct(info, children, ref, conflict))
}

func (e Math) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e Math) StrictNull() expr.Strict {
	if selectedLogPower(e.Plan.Name()) {
		return expr.StrictNone
	}
	return expr.StrictAfter
}

func (e Math) ValidateStoredProvider() error { return nil }

// Retag remaps the comparison collation and recomputes the wrapper context
// from the retagged arguments.
func (e Math) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	ref, conflict := e.Plan.InputCollation()
	next, err := env.Collation(ref)
	if err != nil {
		return nil, false, err
	}
	if !changed && next == ref {
		return e, false, nil
	}
	if err := checkPresent("stuntgres: invalid remapped math argument", children); err != nil {
		return nil, false, err
	}
	node, err := e.Reconstruct(info, children, next, conflict)
	if err != nil {
		return nil, false, err
	}
	node.WrapperContext = mathWrapperContext(node.Plan.Name(), node.Args)
	return node, true, nil
}

var _ expr.Renderable = Math{}

// Render implements expr.Renderable.
func (e Math) Render() (expr.Rendering, error) {
	spec := callRendering("Math "+e.Plan.Name(), e.Args, e.Plan.Name(), e.Args)
	switch {
	case len(e.Args) > 4:
		spec.Replay.Validate = refuseReplay("stuntgres: invalid stored math arity")
		return spec, nil
	case missingArgument(e.Args):
		spec.Replay.Validate = refuseReplay("stuntgres: missing math operand")
		return spec, nil
	}
	spec.Replay.Validate = e.Validate
	if e.Plan.Name() == "pg_size_pretty" {
		// PostgreSQL displays the selected bigint or numeric overload.
		spec.Quote, spec.Args = expr.QuoteVerbatim, hiddenArgs(e.Args[0])
		spec.Args[0].Cast = e.Args[0].ResultUsage()
		spec.Args[0].Transport, spec.Args[0].TransportType = expr.TransportCast, e.Args[0].ResultUsage()
	}
	return spec, nil
}
