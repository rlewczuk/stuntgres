package functions

import (
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type DatetimeExtract struct {
	expr.Info
	Plan               sqltype.DatetimeExtractPlan
	Args               [2]expr.Expr
	Syntax             parser.FunctionSyntax
	Field              string
	DateWrapperContext bool
}

func (e DatetimeExtract) Arguments() []expr.Expr { return e.Args[:] }

func (e DatetimeExtract) Eval(c expr.Context) (sqltype.Value, error) {
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

func PrepareDatetimeExtract(visible, variadic bool, args []Argument) DatetimeExtractPreparation {
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

func ResolveDatetimeExtract(prepared DatetimeExtractPreparation, operands []Argument, types TemporalTypes) TemporalSelection {
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

func (e DatetimeExtract) Reconstruct(info expr.Info, args [2]expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeExtract, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Args, e.Plan = info, args, plan
	return e, changed, nil
}

// DatetimeExtract implements expr.Composite. Rebuild keeps the retained plan descriptors
// and any argument slots beyond Children.
var _ expr.Composite = DatetimeExtract{}

func (e DatetimeExtract) Children() []expr.Expr { return e.Arguments() }
func (e DatetimeExtract) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeExtract", children, len(e.Arguments())); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args, keepDescriptor)
	return rebuilt(node, err)
}

func (e DatetimeExtract) IntrinsicVolatility() expr.Volatility {
	return stableIf(e.Plan.SessionDependent())
}

func (e DatetimeExtract) ValidateStoredProvider() error { return nil }

// Retag rebinds the plan's retained descriptors; a plan change counts as a
// change.
func (e DatetimeExtract) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	args := e.Args
	copy(args[:], children)
	node, planChanged, err := e.Reconstruct(info, args, env.Type)
	return retagged(node, changed || planChanged, err)
}
