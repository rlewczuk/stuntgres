package functions

import (
	"fmt"
	"math"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type RandomContext interface {
	Context
	FunctionRandomOwner() (RandomOwner, error)
}
type Random[E Expression[C], C RandomContext, P any] struct {
	Info[P]
	Name string
	Args []E
}

func (x Random[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func SelectRandom[E interface{ ResultUsage() sqltype.Usage }](p RandomPreparation, args []E, unknown []bool, types NumericTypes) (RandomSelection, Resolution) {
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
func CoerceRandomArguments[E interface{ ResultUsage() sqltype.Usage }, B ArgumentCoercer[E]](b B, raw []parser.Expr, args []E, unknown []bool, selected RandomSelection) error {
	for i, arg := range args {
		argument := Argument[E]{Expression: arg, Raw: raw[i]}
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
