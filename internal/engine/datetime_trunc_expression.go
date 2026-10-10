package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type datetimeTruncExpression struct {
	expressionInfo
	plan  sqltype.DatetimeTruncPlan
	args  [3]BoundExpr
	count int
}

func (e datetimeTruncExpression) children() []BoundExpr { return e.args[:e.count] }
func (e datetimeTruncExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.children() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Evaluate(c.Context, values[:e.count], c.Input)
}
func (b *expressionBinder) bindDatetimeTrunc(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = datetimeFormattingTypeName(o)
		}
		return result
	}
	signature := func() (BoundExpr, error) { return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names()) }
	if !visible || raw.Variadic || (len(operands) != 2 && len(operands) != 3) {
		return signature()
	}
	for _, i := range []int{0, 2} {
		if i >= len(operands) {
			continue
		}
		if d := operands[i].descriptor(); d != nil && d.BaseType().ID() != sqltype.Text {
			if _, err := sqltype.PlanCoercion(operands[i].node.ResultUsage(), b.typ(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	selected := sqltype.Timestamptz
	if len(operands) == 3 {
		if d := operands[1].descriptor(); d != nil {
			source := d.BaseType().ID()
			if source == sqltype.Timestamp || source == sqltype.Date {
				return nil, expressionError("0A000", "date_trunc with time zone requires timestamp with time zone", raw.Pos)
			}
			if source != sqltype.Timestamptz {
				return signature()
			}
		}
	} else {
		candidates := [3]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}
		var viable [3]bool
		bestExact, bestPreferred := -1, -1
		for i, id := range candidates {
			exact, preferred := 0, 0
			if d := operands[1].descriptor(); d != nil {
				source := d.BaseType().ID()
				if source == id {
					exact = 1
				} else {
					if !(source == sqltype.Time && id == sqltype.Interval) {
						if _, err := sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(id).Usage(), sqltype.Implicit); err != nil {
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
			return signature()
		}
		if matches > 1 {
			err := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(raw.Name), strings.Join(names(), ", ")), raw.Pos).(*sqlstate.Error)
			err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			return nil, err
		}
	}
	input := selected
	if d := operands[1].descriptor(); selected == sqltype.Interval && d != nil && d.BaseType().ID() == sqltype.Time {
		input = sqltype.Time
	}
	e := datetimeTruncExpression{count: len(operands)}
	properties := ExprProperties{Volatility: Immutable}
	if selected == sqltype.Timestamptz && e.count == 2 {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = input
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
	var zone *sqltype.Descriptor
	if e.count == 3 {
		zone = b.typ(sqltype.Text)
	}
	plan, err := sqltype.BindDatetimeTrunc(b.typ(sqltype.Text), b.typ(input), e.usage.Type, zone)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	return e, nil
}
