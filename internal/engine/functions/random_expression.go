package functions

import (
	"fmt"
	"math"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Random struct {
	expr.Info
	Name string
	Args []expr.Expr
}

func (x Random) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range x.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	for i := range x.Args {
		if values[i].IsNull() {
			return sqltype.Null(x.Usage.Type), nil
		}
	}
	owner, err := c.FunctionRandomOwner()
	if err != nil {
		return sqltype.Value{}, err
	}
	if x.Name == "setseed" {
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
		a, b := randomSeedWords(seed)
		owner.SeedWords(a, b)
		return sqltype.VoidValue(x.Usage.Type), nil
	}
	var lower, upper int64
	if x.Name == "random" && len(x.Args) == 2 && x.Usage.Type.ID() != sqltype.Numeric {
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
			return sqltype.Integer(x.Usage.Type, lower)
		}
	}
	generator, err := owner.Borrow()
	if err != nil {
		return sqltype.Value{}, err
	}
	defer owner.Release()
	switch x.Name {
	case "random":
		if len(x.Args) == 0 {
			return sqltype.Float(x.Usage.Type, generator.Float64())
		}
		if x.Usage.Type.ID() == sqltype.Numeric {
			return sqltype.NumericRandom(ctx, values[0], values[1], generator.Uint64)
		}
		return sqltype.Integer(x.Usage.Type, randomInteger(lower, upper, generator))
	case "random_normal":
		mean, deviation := float64(0), float64(1)
		if len(x.Args) > 0 {
			mean, err = values[0].Float()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if len(x.Args) > 1 {
			deviation, err = values[1].Float()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		return sqltype.Float(x.Usage.Type, deviation*generator.NormFloat64()+mean)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid random expression")
}

type RandomPreparation struct {
	valid, bounded bool
	demand         uint8
	result         sqltype.ID
}

func PrepareRandom(name string, visible, variadic bool, count int, unknown []bool) RandomPreparation {
	p := RandomPreparation{valid: visible && !variadic, result: sqltype.Float8}
	switch name {
	case "setseed":
		p.valid = p.valid && count == 1
		p.result = sqltype.Void
	case "random_normal":
		p.valid = p.valid && count <= 2
	case "random":
		p.valid = p.valid && (count == 0 || count == 2)
		p.bounded = count == 2
	}
	if !p.valid {
		return p
	}
	if p.bounded {
		for _, deferred := range unknown {
			if !deferred {
				p.demand = numericTypeBit(sqltype.Int4) | numericTypeBit(sqltype.Int8) | numericTypeBit(sqltype.Numeric)
				break
			}
		}
	} else if count != 0 {
		p.demand = numericTypeBit(sqltype.Float8)
	}
	return p
}
func (p RandomPreparation) Needs(id sqltype.ID) bool { return p.demand&numericTypeBit(id) != 0 }

type RandomSelection struct {
	Result    sqltype.ID
	Target    sqltype.Usage
	Coercions [2]sqltype.CoercionPlan
}

func SelectRandom(p RandomPreparation, args []expr.Expr, unknown []bool, types NumericTypes) (RandomSelection, Resolution) {
	selected := RandomSelection{Result: p.result, Target: types.Float8}
	if !p.valid {
		return selected, Missing
	}
	if p.bounded {
		bestScore, count := -1, 0
		var best sqltype.ID
		for _, candidate := range [...]sqltype.ID{sqltype.Int4, sqltype.Int8, sqltype.Numeric} {
			viable, score := true, 0
			for i, arg := range args {
				if unknown[i] {
					continue
				}
				if arg.ResultUsage().Type.BaseType().ID() == candidate {
					score++
				}
				if _, err := sqltype.PlanCoercion(arg.ResultUsage(), types.usage(candidate), sqltype.Implicit); err != nil {
					viable = false
					break
				}
			}
			if !viable {
				continue
			}
			if score > bestScore {
				bestScore = score
				count = 0
			}
			if score == bestScore {
				best = candidate
				count++
			}
		}
		if count == 0 {
			return selected, Missing
		}
		if count != 1 {
			return selected, Ambiguous
		}
		selected.Result = best
		selected.Target = types.usage(best)
	}
	// Resolve the complete signature before coercing any child.
	for i, arg := range args {
		if unknown[i] {
			continue
		}
		var err error
		selected.Coercions[i], err = sqltype.PlanCoercion(arg.ResultUsage(), selected.Target, sqltype.Implicit)
		if err != nil {
			return selected, Missing
		}
	}
	return selected, Resolved
}
func CoerceRandomArguments(b ArgumentCoercer, raw []parser.Expr, args []expr.Expr, unknown []bool, selected RandomSelection) error {
	for i, arg := range args {
		argument := Argument{Expression: arg, Raw: raw[i]}
		if !unknown[i] {
			argument.Usage = arg.ResultUsage()
		}
		value, err := b.CoerceArgument(argument, CoercionRequest{Intent: DirectFormalArgument, Target: selected.Target, Plan: selected.Coercions[i], Planned: !unknown[i], PreserveProperties: true, AtRaw: true})
		if err != nil {
			return err
		}
		args[i] = value
	}
	return nil
}

// Random implements expr.Composite. It has no provider to revalidate, so Rebuild
// only takes the new metadata and children.
var _ expr.Composite = Random{}

func (e Random) Children() []expr.Expr { return e.Args }
func (e Random) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Random", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, append([]expr.Expr(nil), children...)
	return e, nil
}

// IntrinsicVolatility is the analyzed volatility: the call is volatile or
// has no children, so nothing from eliminated arms can be retained.
func (e Random) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }

func (e Random) PlanningFold() expr.Fold { return expr.FoldNever }

// StrictNull preserves required constant-child planning before erasing the call.
func (e Random) StrictNull() expr.Strict { return expr.StrictAfterBare }

var _ expr.Renderable = Random{}

// Render implements expr.Renderable.
func (e Random) Render() (expr.Rendering, error) {
	spec := callRendering("Random "+e.Name, e.Args, e.Name, e.Args)
	spec.Replay.RelationOnly = true
	spec.Replay.Validate = e.replayFault
	return spec, nil
}

// replayFault validates the stored signature: the result selects the
// argument type of random(min, max).
func (e Random) replayFault() error {
	var result sqltype.ID
	if e.Usage.Type != nil {
		result = e.Usage.Type.ID()
	}
	argumentType := sqltype.Float8
	valid := false
	switch e.Name {
	case "random":
		if len(e.Args) == 0 {
			valid = result == sqltype.Float8
		}
		if len(e.Args) == 2 {
			argumentType = result
			valid = argumentType == sqltype.Int4 || argumentType == sqltype.Int8 || argumentType == sqltype.Numeric
		}
	case "random_normal":
		valid = len(e.Args) <= 2 && result == sqltype.Float8
	case "setseed":
		valid = len(e.Args) == 1 && result == sqltype.Void
	}
	if !valid {
		return fmt.Errorf("stuntgres: invalid stored random signature")
	}
	for _, arg := range e.Args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentType {
			return fmt.Errorf("stuntgres: invalid stored random argument")
		}
	}
	return nil
}

// SameGroup implements expr.GroupIdentity.
func (e Random) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Random)
	if !ok || e.Name != o.Name || len(e.Args) != len(o.Args) {
		return false
	}
	for i := range e.Args {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

// PreflightFold implements expr.Preflighting.
func (e Random) PreflightFold() expr.Preflight { return expr.PreflightNullBare }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e Random) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e Random) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Random", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e Random) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	args := e.Children()
	return expr.CaptureOperands{Args: args, Count: len(args)}, true, nil
}
