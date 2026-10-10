package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeOverlapsExpression struct {
	expressionInfo
	args           [4]BoundExpr
	plan           sqltype.DatetimeOverlapsPlan
	syntax         parser.FunctionSyntax
	wrapperContext bool
	// Only physical-literal immutable additions enter this cache, never parameters.
	folded [2]sqltype.Value
}

func (e datetimeOverlapsExpression) children() []BoundExpr { return e.args[:] }
func (e datetimeOverlapsExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	value, pair, err := e.plan.Evaluate(c.Context, values, e.folded, c.Input)
	if err != nil {
		if context := e.effectiveContext(c, pair); context != "" {
			err = overlapsErrorContext(err, context)
		}
	}
	return value, err
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

func (b *expressionBinder) overlapsImplicit(source sqltype.Usage, target sqltype.ID) bool {
	if source.Type.BaseType().ID() == sqltype.Time && target == sqltype.Interval {
		return true
	}
	_, err := sqltype.PlanCoercion(source, b.typ(target).Usage(), sqltype.Implicit)
	return err == nil
}

func (b *expressionBinder) bindDatetimeOverlaps(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var stack [4]comparisonOperand
	operands := stack[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	resolution := func(ambiguous bool) (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		return nil, functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
	}
	if !visible || raw.Variadic || len(raw.Args) != 4 {
		return resolution(false)
	}
	var candidates [13][4]sqltype.ID
	n := 0
	for _, endpoint := range [...]sqltype.ID{sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz} {
		for mask := range 4 {
			candidates[n] = [4]sqltype.ID{endpoint, endpoint, endpoint, endpoint}
			if mask&1 != 0 {
				candidates[n][1] = sqltype.Interval
			}
			if mask&2 != 0 {
				candidates[n][3] = sqltype.Interval
			}
			n++
		}
	}
	candidates[12] = [4]sqltype.ID{sqltype.Timetz, sqltype.Timetz, sqltype.Timetz, sqltype.Timetz}
	var viable [13]bool
	selected := -1
	// Exact function matching does not treat UNKNOWN as an exact match.
	for j, signature := range candidates {
		exact := true
		for i, target := range signature {
			d := operands[i].descriptor()
			if d == nil || d.ID() != target {
				exact = false
				break
			}
		}
		if exact {
			selected = j
			break
		}
	}
	if selected < 0 {
		bestExact, bestPreferred := -1, -1
		for j, signature := range candidates {
			ok, exact, preferred := true, 0, 0
			for i, target := range signature {
				o := operands[i]
				d := o.descriptor()
				if d == nil {
					continue
				}
				source := d.BaseType().ID()
				if source == target {
					exact++
					continue
				}
				if !b.overlapsImplicit(o.node.ResultUsage(), target) {
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
			if o.descriptor() != nil {
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
					if o.descriptor() == nil && (overlapsCategory(signature[i]) != categories[i] || preferred[i] && !overlapsPreferred(signature[i])) {
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
			return resolution(false)
		}
		if count > 1 {
			// Last gasp runs even after category conflict, never as an early operator shortcut.
			known := sqltype.ID(0)
			for _, o := range operands {
				if d := o.descriptor(); d != nil {
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
						if !b.overlapsImplicit(b.typ(known).Usage(), target) {
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
				return resolution(true)
			}
		}
	}
	signature := candidates[selected]
	e := datetimeOverlapsExpression{syntax: raw.Syntax}
	properties := ExprProperties{Volatility: Immutable}
	var local [4]bool
	for i, o := range operands {
		target := b.typ(signature[i])
		if d := o.descriptor(); d != nil && d.BaseType().ID() == sqltype.Time && signature[i] == sqltype.Interval {
			target = b.typ(sqltype.Time)
			local[i] = true
		}
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target.Usage() {
			coercion, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), coercionProperties(arg, coercion)), arg, coercion, sqltype.Implicit}
		}
		e.args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	plan, err := sqltype.BindDatetimeOverlaps(b.typ(signature[0]), b.typ(sqltype.Interval), b.typ(sqltype.Bool), b.typ(sqltype.Time), [2]bool{signature[1] == sqltype.Interval, signature[3] == sqltype.Interval}, local)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	if plan.SessionDependent() {
		properties.Volatility = max(properties.Volatility, Stable)
	}
	e.expressionInfo = b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties)
	e.wrapperContext = !overlapsInlineSafe(e)
	return e, nil
}
