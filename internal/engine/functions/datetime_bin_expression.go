package functions

import (
	"stuntgres/internal/sqltype"
)

type DatetimeBin[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan sqltype.DatetimeBinPlan
	Args [3]E
}

func (e DatetimeBin[E, C, P]) Arguments() []E { return e.Args[:] }

func (e DatetimeBin[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.Plan.Evaluate(ctx, values[:])
}

var datetimeBinCandidates = [...]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz}

type DatetimeBinPreparation struct {
	candidates []sqltype.ID
	Demand     TemporalDemand
}

func PrepareDatetimeBin[E any](visible, variadic bool, args []Argument[E]) DatetimeBinPreparation {
	if !visible || variadic || len(args) != 3 {
		return DatetimeBinPreparation{}
	}
	p := DatetimeBinPreparation{candidates: datetimeBinCandidates[:]}
	if args[0].Usage.Type != nil {
		p.Demand |= NeedInterval
	}
	for _, id := range p.candidates {
		for _, arg := range args[1:] {
			if d := arg.Usage.Type; d != nil && d.BaseType().ID() != id {
				p.Demand |= temporalDemand(id)
			}
		}
	}
	return p
}

func ResolveDatetimeBin[E any](prepared DatetimeBinPreparation, operands []Argument[E], types TemporalTypes) TemporalSelection {
	if len(prepared.candidates) == 0 {
		return TemporalSelection{Resolution: Missing}
	}

	if operands[0].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, types.Usage(sqltype.Interval), sqltype.Implicit); err != nil {
			return TemporalSelection{Resolution: Missing}
		}
	}
	// The only candidates have the same INTERVAL first slot and datetime
	// category. Known exact matches precede preferred conversions; unknown
	// temporal slots break a remaining tie in favor of TIMESTAMPTZ.
	candidates := prepared.candidates
	var viable [2]bool
	bestExact, bestPreferred := -1, -1
	unknown := false
	for _, o := range operands[1:] {
		unknown = unknown || o.Usage.Type == nil
	}
	for j, id := range candidates {
		ok, exact, preferred := true, 0, 0
		for _, o := range operands[1:] {
			d := o.Usage.Type
			if d == nil {
				continue
			}
			if d.BaseType().ID() == id {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(o.Usage, types.Usage(id), sqltype.Implicit); err != nil {
				ok = false
				break
			}
			if id == sqltype.Timestamptz {
				preferred++
			}
		}
		if !ok {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [2]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	if unknown && viable[1] {
		viable[0] = false
	}
	selected := sqltype.Timestamp
	if viable[1] {
		selected = sqltype.Timestamptz
	} else if !viable[0] {
		return TemporalSelection{Resolution: Missing}
	}

	return TemporalSelection{Selected: selected, Input: selected}
}

func (e DatetimeBin[E, C, P]) Reconstruct(info Info[P], args [3]E, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeBin[E, C, P], bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}
