package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type DatetimeAgeFinite struct {
	expr.Info
	Plan  sqltype.DatetimeAgeFinitePlan
	Args  [2]expr.Expr
	Count int
}

func (e DatetimeAgeFinite) Arguments() []expr.Expr { return e.Args[:e.Count] }

func (e DatetimeAgeFinite) Eval(c expr.Context) (sqltype.Value, error) {
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

var datetimeAgeCandidates = [...]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz, sqltype.Xid}
var datetimeFiniteCandidates = [...]sqltype.ID{sqltype.Date, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}

type DatetimeAgeFinitePreparation struct {
	candidates []sqltype.ID
	Demand     TemporalDemand
}

func PrepareDatetimeAgeFinite(name string, visible, variadic bool, args []Argument) DatetimeAgeFinitePreparation {
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
				if source != id && id != sqltype.Xid && !(source == sqltype.Time && id == sqltype.Interval) {
					p.Demand |= temporalDemand(id)
				}
			}
		}
	}
	return p
}

func ResolveDatetimeAgeFinite(prepared DatetimeAgeFinitePreparation, name string, operands []Argument, types TemporalTypes) TemporalSelection {
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
			if id == sqltype.Xid {
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

func (e DatetimeAgeFinite) Reconstruct(info expr.Info, args [2]expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeAgeFinite, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}

// DatetimeAgeFinite implements expr.Composite. Rebuild keeps the retained plan descriptors
// and any argument slots beyond Children.
var _ expr.Composite = DatetimeAgeFinite{}

func (e DatetimeAgeFinite) Children() []expr.Expr { return e.Arguments() }
func (e DatetimeAgeFinite) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeAgeFinite", children, len(e.Arguments())); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args, keepDescriptor)
	return rebuilt(node, err)
}

func (e DatetimeAgeFinite) IntrinsicVolatility() expr.Volatility { return stableIf(e.Plan.SessionDependent()) }

func (e DatetimeAgeFinite) StrictNull() expr.Strict { return expr.StrictAfter }

func (e DatetimeAgeFinite) ValidateStoredProvider() error { return nil }
