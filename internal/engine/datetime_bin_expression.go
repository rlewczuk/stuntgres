package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeBinExpression struct {
	expressionInfo
	plan sqltype.DatetimeBinPlan
	args [3]BoundExpr
}

func (e datetimeBinExpression) children() []BoundExpr { return e.args[:] }
func (e datetimeBinExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Evaluate(c.Context, values[:])
}

func (b *expressionBinder) bindDatetimeBin(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [3]comparisonOperand
	operands := small[:min(len(raw.Args), 3)]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
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
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !visible || raw.Variadic || len(operands) != 3 {
		return signature()
	}
	if operands[0].descriptor() != nil {
		if _, err := sqltype.PlanCoercion(operands[0].node.ResultUsage(), b.typ(sqltype.Interval).Usage(), sqltype.Implicit); err != nil {
			return signature()
		}
	}
	// The only candidates have the same INTERVAL first slot and datetime
	// category. Known exact matches precede preferred conversions; unknown
	// temporal slots break a remaining tie in favor of TIMESTAMPTZ.
	candidates := [2]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz}
	var viable [2]bool
	bestExact, bestPreferred := -1, -1
	unknown := false
	for _, o := range operands[1:] {
		unknown = unknown || o.descriptor() == nil
	}
	for j, id := range candidates {
		ok, exact, preferred := true, 0, 0
		for _, o := range operands[1:] {
			d := o.descriptor()
			if d == nil {
				continue
			}
			if d.BaseType().ID() == id {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(id).Usage(), sqltype.Implicit); err != nil {
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
		return signature()
	}
	e := datetimeBinExpression{}
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		id := selected
		if i == 0 {
			id = sqltype.Interval
		}
		target := b.typ(id)
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target.Usage() {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
		}
		e.args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	e.expressionInfo = b.info(b.typ(selected).Usage(), raw.Pos, properties)
	plan, err := sqltype.BindDatetimeBin(b.typ(sqltype.Interval), b.typ(selected), e.usage.Type)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	return e, nil
}
