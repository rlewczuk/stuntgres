package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type DatetimeOverlaps struct {
	expr.Info
	Args           [4]expr.Expr
	Plan           sqltype.DatetimeOverlapsPlan
	Syntax         parser.FunctionSyntax
	WrapperContext bool
	// Only physical-literal immutable additions enter this cache, never parameters.
	Folded [2]sqltype.Value
}

func (e DatetimeOverlaps) Arguments() []expr.Expr { return e.Args[:] }

func (e DatetimeOverlaps) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	value, pair, err := e.Plan.Evaluate(ctx, values, e.Folded, c.FunctionInput())
	if err != nil {
		if context := e.ErrorContext(c, pair); context != "" {
			err = OverlapsErrorContext(err, context)
		}
	}
	return value, err
}
func (e DatetimeOverlaps) ErrorContext(c expr.Context, pair int) string {
	if pair < 0 {
		return ""
	}
	var constant [4]bool
	all := true
	for i, arg := range e.Args {
		constant[i] = c.FunctionOverlapsConstant(arg)
		all = all && constant[i]
	}
	if !e.Plan.SessionDependent() && all || e.WrapperContext {
		return `SQL function "overlaps" statement 1`
	}
	if !e.Plan.SessionDependent() && constant[pair*2] && constant[pair*2+1] {
		return `SQL function "overlaps" during inlining`
	}
	return ""
}

func overlapsCategory(id sqltype.ID) byte {
	switch id {
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz:
		return 'D'
	case sqltype.Interval:
		return 'T'
	}
	return 'U'
}
func overlapsPreferred(id sqltype.ID) bool {
	return id == sqltype.Timestamptz || id == sqltype.Interval
}

func overlapsImplicit(types TemporalTypes, source sqltype.Usage, target sqltype.ID) bool {
	if source.Type.BaseType().ID() == sqltype.Time && target == sqltype.Interval {
		return true
	}
	_, err := sqltype.PlanCoercion(source, types.Usage(target), sqltype.Implicit)
	return err == nil
}

type OverlapsSelection struct {
	Signature  [4]sqltype.ID
	Resolution Resolution
}

var datetimeOverlapsCandidates = [...][4]sqltype.ID{
	{sqltype.Time, sqltype.Time, sqltype.Time, sqltype.Time},
	{sqltype.Time, sqltype.Interval, sqltype.Time, sqltype.Time},
	{sqltype.Time, sqltype.Time, sqltype.Time, sqltype.Interval},
	{sqltype.Time, sqltype.Interval, sqltype.Time, sqltype.Interval},
	{sqltype.Timestamp, sqltype.Timestamp, sqltype.Timestamp, sqltype.Timestamp},
	{sqltype.Timestamp, sqltype.Interval, sqltype.Timestamp, sqltype.Timestamp},
	{sqltype.Timestamp, sqltype.Timestamp, sqltype.Timestamp, sqltype.Interval},
	{sqltype.Timestamp, sqltype.Interval, sqltype.Timestamp, sqltype.Interval},
	{sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Timestamptz},
	{sqltype.Timestamptz, sqltype.Interval, sqltype.Timestamptz, sqltype.Timestamptz},
	{sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Interval},
	{sqltype.Timestamptz, sqltype.Interval, sqltype.Timestamptz, sqltype.Interval},
	{sqltype.Timetz, sqltype.Timetz, sqltype.Timetz, sqltype.Timetz},
}

type DatetimeOverlapsPreparation struct {
	candidates [][4]sqltype.ID
	exact      int
	Demand     TemporalDemand
}

func PrepareDatetimeOverlaps(visible, variadic bool, args []Argument) DatetimeOverlapsPreparation {
	if !visible || variadic || len(args) != 4 {
		return DatetimeOverlapsPreparation{}
	}
	p := DatetimeOverlapsPreparation{candidates: datetimeOverlapsCandidates[:], exact: -1}
	for j, signature := range p.candidates {
		exact := true
		for i, target := range signature {
			d := args[i].Usage.Type
			if d == nil || d.ID() != target {
				exact = false
				break
			}
		}
		if exact {
			p.exact = j
			return p
		}
	}
	known := sqltype.ID(0)
	for _, arg := range args {
		if d := arg.Usage.Type; d != nil {
			id := d.BaseType().ID()
			if known != 0 && known != id {
				known = 0
				break
			}
			known = id
		}
	}
	// The possible last-gasp source uses the same engine-supplied default usage.
	if overlapsCategory(known) != 'U' {
		p.Demand |= temporalDemand(known)
	}
	for _, signature := range p.candidates {
		for i, target := range signature {
			if d := args[i].Usage.Type; d != nil {
				source := d.BaseType().ID()
				if source != target && !(source == sqltype.Time && target == sqltype.Interval) {
					p.Demand |= temporalDemand(target)
				}
			}
		}
	}
	return p
}

func ResolveDatetimeOverlaps(prepared DatetimeOverlapsPreparation, operands []Argument, types TemporalTypes) OverlapsSelection {
	if len(prepared.candidates) == 0 {
		return OverlapsSelection{Resolution: Missing}
	}

	candidates := prepared.candidates
	var viable [13]bool
	selected := prepared.exact
	if selected < 0 {
		bestExact, bestPreferred := -1, -1
		for j, signature := range candidates {
			ok, exact, preferred := true, 0, 0
			for i, target := range signature {
				o := operands[i]
				d := o.Usage.Type
				if d == nil {
					continue
				}
				source := d.BaseType().ID()
				if source == target {
					exact++
					continue
				}
				if !overlapsImplicit(types, o.Usage, target) {
					ok = false
					break
				}
				if overlapsCategory(source) == overlapsCategory(target) && overlapsPreferred(target) {
					preferred++
				}
			}
			if !ok {
				continue
			}
			if exact > bestExact || exact == bestExact && preferred > bestPreferred {
				viable = [13]bool{}
				bestExact, bestPreferred = exact, preferred
			}
			if exact == bestExact && preferred == bestPreferred {
				viable[j] = true
			}
		}
		var categories [4]byte
		var preferred [4]bool
		resolved := true
		for i, o := range operands {
			if o.Usage.Type != nil {
				continue
			}
			for j, signature := range candidates {
				if !viable[j] {
					continue
				}
				category := overlapsCategory(signature[i])
				if categories[i] != 0 && categories[i] != category {
					resolved = false
				}
				categories[i] = category
				preferred[i] = preferred[i] || overlapsPreferred(signature[i])
			}
		}
		if resolved {
			for j, signature := range candidates {
				if !viable[j] {
					continue
				}
				for i, o := range operands {
					if o.Usage.Type == nil && (overlapsCategory(signature[i]) != categories[i] || preferred[i] && !overlapsPreferred(signature[i])) {
						viable[j] = false
						break
					}
				}
			}
		}
		count := 0
		for j, v := range viable {
			if v {
				selected = j
				count++
			}
		}
		if count == 0 {
			return OverlapsSelection{Resolution: Missing}
		}
		if count > 1 {
			// Last gasp runs even after category conflict, never as an early operator shortcut.
			known := sqltype.ID(0)
			for _, o := range operands {
				if d := o.Usage.Type; d != nil {
					id := d.BaseType().ID()
					if known != 0 && known != id {
						known = 0
						break
					}
					known = id
				}
			}
			if known != 0 {
				matches, choice := 0, -1
				for j, signature := range candidates {
					if !viable[j] {
						continue
					}
					ok := true
					for _, target := range signature {
						if !overlapsImplicit(types, types.Usage(known), target) {
							ok = false
							break
						}
					}
					if ok {
						matches++
						choice = j
					}
				}
				if matches == 1 {
					selected = choice
					count = 1
				}
			}
			if count > 1 {
				return OverlapsSelection{Resolution: Ambiguous}
			}
		}
	}
	return OverlapsSelection{Signature: candidates[selected]}

}
func OverlapsErrorContext(err error, context string) error {
	if d, ok := err.(*sqlstate.Error); ok && d.Context == "" {
		copy := *d
		copy.Context = context
		return &copy
	}
	return err
}

func DatetimeOverlapsResult() sqltype.ID { return sqltype.Bool }

func (e DatetimeOverlaps) Reconstruct(info expr.Info, args [4]expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor, remapValue func(sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)) (DatetimeOverlaps, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	folded := e.Folded
	changed := plan != e.Plan
	for pair, value := range e.Folded {
		if value.Type() == nil {
			continue
		}
		retagged, err := remapValue(value, plan.ArgumentType(pair*2))
		if err != nil {
			return e, false, err
		}
		folded[pair] = retagged
		changed = changed || retagged != value
	}
	e.Info, e.Args, e.Plan, e.Folded = info, args, plan, folded
	return e, changed, nil
}

// DatetimeOverlaps implements expr.Composite. Rebuild keeps the retained plan
// descriptors and folded pair ends.
var _ expr.Composite = DatetimeOverlaps{}

func (e DatetimeOverlaps) Children() []expr.Expr { return e.Args[:] }
func (e DatetimeOverlaps) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeOverlaps", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args, keepDescriptor, keepValue)
	return rebuilt(node, err)
}
