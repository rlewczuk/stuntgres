package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type DatetimeBin struct {
	expr.Info
	Plan sqltype.DatetimeBinPlan
	Args [3]expr.Expr
}

func (e DatetimeBin) Eval(c expr.Context) (sqltype.Value, error) {
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

func PrepareDatetimeBin(visible, variadic bool, args []Argument) DatetimeBinPreparation {
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

func ResolveDatetimeBin(prepared DatetimeBinPreparation, operands []Argument, types TemporalTypes) TemporalSelection {
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

func (e DatetimeBin) Reconstruct(info expr.Info, args [3]expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeBin, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}

// DatetimeBin implements expr.Composite. Rebuild keeps the retained plan descriptors
// and any argument slots beyond Children.
var _ expr.Composite = DatetimeBin{}

func (e DatetimeBin) Children() []expr.Expr { return e.Args[:] }
func (e DatetimeBin) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeBin", children, len(e.Args[:])); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args, keepDescriptor)
	return rebuilt(node, err)
}

func (e DatetimeBin) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e DatetimeBin) StrictNull() expr.Strict { return expr.StrictAfter }

func (e DatetimeBin) ValidateStoredProvider() error { return nil }

// Retag rebinds the plan's retained descriptors; a plan change counts as a
// change.
func (e DatetimeBin) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	args := e.Args
	copy(args[:], children)
	node, planChanged, err := e.Reconstruct(info, args, env.Type)
	return retagged(node, changed || planChanged, err)
}

var _ expr.Renderable = DatetimeBin{}

// Render implements expr.Renderable.
func (e DatetimeBin) Render() (expr.Rendering, error) {
	return callRendering("Datetime binning", e.Args[:], "date_bin", e.Args[:]), nil
}

// SameGroup implements expr.GroupIdentity.
func (e DatetimeBin) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(DatetimeBin)
	if !ok || e.Plan != o.Plan {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e DatetimeBin) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e DatetimeBin) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeBin", children, len(e.Args[:])); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
