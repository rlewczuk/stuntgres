package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type DatetimeTrunc struct {
	expr.Info
	Plan  sqltype.DatetimeTruncPlan
	Args  [3]expr.Expr
	Count int
}

func (e DatetimeTrunc) Arguments() []expr.Expr { return e.Args[:e.Count] }

func (e DatetimeTrunc) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.Arguments() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.Plan.Evaluate(ctx, values[:e.Count], c.FunctionInput())
}

var datetimeTruncCandidates = [...]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}

type DatetimeTruncPreparation struct {
	candidates []sqltype.ID
	valid      bool
	Demand     TemporalDemand
}

func PrepareDatetimeTrunc(visible, variadic bool, args []Argument) DatetimeTruncPreparation {
	if !visible || variadic || (len(args) != 2 && len(args) != 3) {
		return DatetimeTruncPreparation{}
	}
	p := DatetimeTruncPreparation{valid: true}
	for _, i := range [...]int{0, 2} {
		if i < len(args) {
			if d := args[i].Usage.Type; d != nil && d.BaseType().ID() != sqltype.Text {
				p.Demand |= NeedText
			}
		}
	}
	if len(args) == 2 {
		p.candidates = datetimeTruncCandidates[:]
		if d := args[1].Usage.Type; d != nil {
			source := d.BaseType().ID()
			for _, id := range p.candidates {
				if source != id && !(source == sqltype.Time && id == sqltype.Interval) {
					p.Demand |= temporalDemand(id)
				}
			}
		}
	}
	return p
}

func ResolveDatetimeTrunc(prepared DatetimeTruncPreparation, operands []Argument, types TemporalTypes) TemporalSelection {
	if !prepared.valid {
		return TemporalSelection{Resolution: Missing}
	}

	for _, i := range []int{0, 2} {
		if i >= len(operands) {
			continue
		}
		if d := operands[i].Usage.Type; d != nil && d.BaseType().ID() != sqltype.Text {
			if _, err := sqltype.PlanCoercion(operands[i].Usage, types.Usage(sqltype.Text), sqltype.Implicit); err != nil {
				return TemporalSelection{Resolution: Missing}
			}
		}
	}
	selected := sqltype.Timestamptz
	if len(operands) == 3 {
		if d := operands[1].Usage.Type; d != nil {
			source := d.BaseType().ID()
			if source == sqltype.Timestamp || source == sqltype.Date {
				return TemporalSelection{Unsupported: "date_trunc with time zone requires timestamp with time zone"}
			}
			if source != sqltype.Timestamptz {
				return TemporalSelection{Resolution: Missing}
			}
		}
	} else {
		candidates := prepared.candidates
		var viable [3]bool
		bestExact, bestPreferred := -1, -1
		for i, id := range candidates {
			exact, preferred := 0, 0
			if d := operands[1].Usage.Type; d != nil {
				source := d.BaseType().ID()
				if source == id {
					exact = 1
				} else {
					if !(source == sqltype.Time && id == sqltype.Interval) {
						if _, err := sqltype.PlanCoercion(operands[1].Usage, types.Usage(id), sqltype.Implicit); err != nil {
							continue
						}
					}
					if id == sqltype.Timestamptz {
						preferred = 1
					}
				}
			}
			if exact > bestExact || exact == bestExact && preferred > bestPreferred {
				viable = [3]bool{}
				bestExact, bestPreferred = exact, preferred
			}
			if exact == bestExact && preferred == bestPreferred {
				viable[i] = true
			}
		}
		matches := 0
		for i, id := range candidates {
			if viable[i] {
				selected = id
				matches++
			}
		}
		if matches == 0 {
			return TemporalSelection{Resolution: Missing}
		}
		if matches > 1 {
			return TemporalSelection{Resolution: Ambiguous}
		}
	}
	input := selected
	if d := operands[1].Usage.Type; selected == sqltype.Interval && d != nil && d.BaseType().ID() == sqltype.Time {
		input = sqltype.Time
	}

	return TemporalSelection{Selected: selected, Input: input}
}

func DatetimeTruncIntrinsicStable(selected sqltype.ID, count int) bool {
	return selected == sqltype.Timestamptz && count == 2
}

func (e DatetimeTrunc) Reconstruct(info expr.Info, args [3]expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeTrunc, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}

// DatetimeTrunc implements expr.Composite. Rebuild keeps the retained plan descriptors
// and any argument slots beyond Children.
var _ expr.Composite = DatetimeTrunc{}

func (e DatetimeTrunc) Children() []expr.Expr { return e.Arguments() }
func (e DatetimeTrunc) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeTrunc", children, len(e.Arguments())); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args, keepDescriptor)
	return rebuilt(node, err)
}

func (e DatetimeTrunc) IntrinsicVolatility() expr.Volatility {
	return stableIf(e.Plan.SessionDependent())
}

func (e DatetimeTrunc) StrictNull() expr.Strict { return expr.StrictAfter }

func (e DatetimeTrunc) ValidateStoredProvider() error { return nil }

// Retag rebinds the plan's retained descriptors; a plan change counts as a
// change.
func (e DatetimeTrunc) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	args := e.Args
	copy(args[:], children)
	node, planChanged, err := e.Reconstruct(info, args, env.Type)
	return retagged(node, changed || planChanged, err)
}

var _ expr.Renderable = DatetimeTrunc{}

// Render implements expr.Renderable.
func (e DatetimeTrunc) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "Datetime truncation", Inputs: e.Arguments()}, nil
}
