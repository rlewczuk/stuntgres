package functions

import (
	"stuntgres/internal/sqltype"
)

type DatetimeAgeFiniteContext[E any] interface {
	LiteralContext[E]
	FunctionInput() sqltype.InputEnvironment
}

type DatetimeAgeFinite[E Expression[C], C DatetimeAgeFiniteContext[E], P any] struct {
	Info[P]
	Plan  sqltype.DatetimeAgeFinitePlan
	Args  [2]E
	Count int
}

func (e DatetimeAgeFinite[E, C, P]) Arguments() []E { return e.Args[:e.Count] }

func (e DatetimeAgeFinite[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Arguments() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Arguments() {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[:e.Count], c.FunctionInput())
}

const ageDiagnosticXid sqltype.ID = 28

func ageFiniteCategory(id sqltype.ID) byte {
	switch id {
	case sqltype.Date, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Time:
		return 'D'
	case sqltype.Interval:
		return 'T'
	default:
		return 'U'
	}
}
func ageFinitePreferred(id sqltype.ID) bool {
	return id == sqltype.Timestamptz || id == sqltype.Interval
}

var datetimeAgeCandidates = [...]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz, ageDiagnosticXid}
var datetimeFiniteCandidates = [...]sqltype.ID{sqltype.Date, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}

type DatetimeAgeFinitePreparation struct {
	candidates []sqltype.ID
	Demand     TemporalDemand
}

func PrepareDatetimeAgeFinite[E any](name string, visible, variadic bool, args []Argument[E]) DatetimeAgeFinitePreparation {
	if !visible || variadic || len(args) < 1 || len(args) > 2 || name == "isfinite" && len(args) != 1 {
		return DatetimeAgeFinitePreparation{}
	}
	p := DatetimeAgeFinitePreparation{candidates: datetimeAgeCandidates[:2]}
	if name == "isfinite" {
		p.candidates = datetimeFiniteCandidates[:]
	} else if len(args) == 1 {
		p.candidates = datetimeAgeCandidates[:]
	}
	for _, id := range p.candidates {
		for _, arg := range args {
			if d := arg.Usage.Type; d != nil {
				source := d.BaseType().ID()
				if source != id && id != ageDiagnosticXid && !(source == sqltype.Time && id == sqltype.Interval) {
					p.Demand |= temporalDemand(id)
				}
			}
		}
	}
	return p
}

func ResolveDatetimeAgeFinite[E any](prepared DatetimeAgeFinitePreparation, name string, operands []Argument[E], types TemporalTypes) TemporalSelection {
	if len(prepared.candidates) == 0 {
		return TemporalSelection{Resolution: Missing}
	}

	candidates := prepared.candidates
	n := len(candidates)
	var viable [4]bool
	bestExact, bestPreferred := -1, -1
	for j, id := range candidates[:n] {
		ok, exact, preferred := true, 0, 0
		for _, o := range operands {
			d := o.Usage.Type
			if d == nil {
				continue
			}
			source := d.BaseType().ID()
			if source == id {
				exact++
				continue
			}
			if id == ageDiagnosticXid {
				ok = false
				break
			}
			// PG has this implicit cast, but native deliberately does not.
			if source == sqltype.Time && id == sqltype.Interval {
				continue
			}
			if _, err := sqltype.PlanCoercion(o.Usage, types.Usage(id), sqltype.Implicit); err != nil {
				ok = false
				break
			}
			if ageFiniteCategory(source) == ageFiniteCategory(id) && ageFinitePreferred(id) {
				preferred++
			}
		}
		if !ok {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [4]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	// UNKNOWN arguments require a single category. Within that category PG's
	// preferred input wins; no string category exists in this signature family.
	for _, o := range operands {
		if o.Usage.Type != nil {
			continue
		}
		category, preferred, conflict := byte(0), false, false
		for j, id := range candidates[:n] {
			if !viable[j] {
				continue
			}
			cat := ageFiniteCategory(id)
			if category != 0 && category != cat {
				conflict = true
			}
			category = cat
			preferred = preferred || ageFinitePreferred(id)
		}
		if conflict {
			break
		}
		if preferred {
			for j, id := range candidates[:n] {
				if viable[j] && !ageFinitePreferred(id) {
					viable[j] = false
				}
			}
		}
	}
	selected, matches := sqltype.ID(0), 0
	for j, id := range candidates[:n] {
		if viable[j] {
			selected, matches = id, matches+1
		}
	}
	if matches == 0 {
		return TemporalSelection{Resolution: Missing}
	}
	if matches > 1 {
		return TemporalSelection{Resolution: Ambiguous}
	}
	if selected == ageDiagnosticXid {
		return TemporalSelection{Unsupported: "age with xid is unsupported"}
	}
	if selected == sqltype.Timestamptz {
		return TemporalSelection{Unsupported: name + " with time zone is unsupported"}
	}
	if selected == sqltype.Interval && operands[0].Usage.Type != nil && operands[0].Usage.Type.BaseType().ID() == sqltype.Time {
		return TemporalSelection{Unsupported: "time to interval conversion is unsupported"}
	}

	return TemporalSelection{Selected: selected, Input: selected}
}

func DatetimeAgeFiniteIntrinsicStable(name string, count int) bool {
	return name == "age" && count == 1
}

func DatetimeAgeFiniteResult(name string) sqltype.ID {
	if name == "isfinite" {
		return sqltype.Bool
	}
	return sqltype.Interval
}

func (e DatetimeAgeFinite[E, C, P]) Reconstruct(info Info[P], args [2]E, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeAgeFinite[E, C, P], bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}
