package functions

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type DatetimeExtractContext[E any] interface {
	Context
	FunctionInput() sqltype.InputEnvironment
}

type DatetimeExtract[E Expression[C], C DatetimeExtractContext[E], P any] struct {
	Info[P]
	Plan               sqltype.DatetimeExtractPlan
	Args               [2]E
	Syntax             parser.FunctionSyntax
	Field              string
	DateWrapperContext bool
}

func (e DatetimeExtract[E, C, P]) Arguments() []E { return e.Args[:] }

func (e DatetimeExtract[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	value, err := e.Plan.Evaluate(ctx, values[:], c.FunctionInput())
	if err != nil && e.DateWrapperContext {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Context = `SQL function "date_part" statement 1`
			err = &copy
		}
	}
	return value, err
}

var datetimeExtractCandidates = [...]sqltype.ID{sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}

type DatetimeExtractPreparation struct {
	candidates []sqltype.ID
	Demand     TemporalDemand
}

func PrepareDatetimeExtract[E any](visible, variadic bool, args []Argument[E]) DatetimeExtractPreparation {
	if !visible || variadic || len(args) != 2 {
		return DatetimeExtractPreparation{}
	}
	p := DatetimeExtractPreparation{candidates: datetimeExtractCandidates[:]}
	if d := args[0].Usage.Type; d != nil && d.BaseType().ID() != sqltype.Text {
		p.Demand |= NeedText
	}
	if d := args[1].Usage.Type; d != nil {
		for _, id := range p.candidates {
			if d.BaseType().ID() != id {
				p.Demand |= temporalDemand(id)
			}
		}
	}
	return p
}

func ResolveDatetimeExtract[E any](prepared DatetimeExtractPreparation, operands []Argument[E], types TemporalTypes) TemporalSelection {
	if len(prepared.candidates) == 0 {
		return TemporalSelection{Resolution: Missing}
	}

	if d := operands[0].Usage.Type; d != nil && d.BaseType().ID() != sqltype.Text {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, types.Usage(sqltype.Text), sqltype.Implicit); err != nil {
			return TemporalSelection{Resolution: Missing}
		}
	}
	candidates := prepared.candidates
	var viable [6]bool
	bestExact, bestPreferred := -1, -1
	for i, id := range candidates {
		exact, preferred := 0, 0
		if d := operands[1].Usage.Type; d != nil {
			source := d.BaseType().ID()
			if source == id {
				exact = 1
			} else {
				if _, err := sqltype.PlanCoercion(operands[1].Usage, types.Usage(id), sqltype.Implicit); err != nil {
					continue
				}
				if source != sqltype.Interval && id == sqltype.Timestamptz {
					preferred = 1
				}
			}
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [6]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[i] = true
		}
	}
	if operands[1].Usage.Type == nil {
		// Interval and datetime categories conflict for an unknown source. Only
		// within an unambiguous datetime category may TSTZ be preferred.
		datetime, interval := false, false
		for i, id := range candidates {
			if viable[i] {
				if id == sqltype.Interval {
					interval = true
				} else {
					datetime = true
				}
			}
		}
		if datetime && !interval {
			for i, id := range candidates {
				if id != sqltype.Timestamptz {
					viable[i] = false
				}
			}
		}
	}
	selected, matches := sqltype.ID(0), 0
	for i, id := range candidates {
		if viable[i] {
			selected, matches = id, matches+1
		}
	}
	if matches == 0 {
		return TemporalSelection{Resolution: Missing}
	}
	if matches > 1 {
		return TemporalSelection{Resolution: Ambiguous}
	}

	return TemporalSelection{Selected: selected, Input: selected}
}

func DatetimeExtractIntrinsicStable(selected sqltype.ID) bool { return selected == sqltype.Timestamptz }

func DatetimeExtractResult(name string) sqltype.ID {
	if name == "date_part" {
		return sqltype.Float8
	}
	return sqltype.Numeric
}

func DatetimeExtractWrapperContext(name string, selected sqltype.ID, immutableArguments bool) bool {
	return name == "date_part" && selected == sqltype.Date && immutableArguments
}

func (e DatetimeExtract[E, C, P]) Reconstruct(info Info[P], args [2]E, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeExtract[E, C, P], bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}
