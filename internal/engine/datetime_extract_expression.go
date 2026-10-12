package engine

import (
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type datetimeExtractExpression struct {
	expressionInfo
	plan               sqltype.DatetimeExtractPlan
	args               [2]BoundExpr
	syntax             parser.FunctionSyntax
	field              string
	dateWrapperContext bool
}

func (e datetimeExtractExpression) children() []BoundExpr { return e.args[:] }
func (e datetimeExtractExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	value, err := e.plan.Evaluate(c.Context, values[:], c.Input)
	if err != nil && e.dateWrapperContext {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Context = `SQL function "date_part" statement 1`
			err = &copy
		}
	}
	return value, err
}

func (b *expressionBinder) bindDatetimeExtract(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]comparisonOperand
	operands := small[:min(len(raw.Args), 2)]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
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
	diagnosticName := raw.Name
	if raw.Syntax == parser.SQLFunctionSyntax {
		diagnosticName = parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: raw.Pos}, {Text: "extract", Pos: raw.Pos}}}
	}
	names := func() []string {
		result := make([]string, len(operands))
		for i, o := range operands {
			result[i] = datetimeFormattingTypeName(o)
		}
		return result
	}
	signature := func() (BoundExpr, error) {
		return nil, b.functionSignatureErrorForTypes(diagnosticName, raw.Pos, names())
	}
	if !visible || raw.Variadic || len(operands) != 2 {
		return signature()
	}
	if d := operands[0].descriptor(); d != nil && d.BaseType().ID() != sqltype.Text {
		if _, err := sqltype.PlanCoercion(operands[0].node.ResultUsage(), b.typ(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
			return signature()
		}
	}
	candidates := [6]sqltype.ID{sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}
	var viable [6]bool
	bestExact, bestPreferred := -1, -1
	for i, id := range candidates {
		exact, preferred := 0, 0
		if d := operands[1].descriptor(); d != nil {
			source := d.BaseType().ID()
			if source == id {
				exact = 1
			} else {
				if _, err := sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(id).Usage(), sqltype.Implicit); err != nil {
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
	if operands[1].descriptor() == nil {
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
		return signature()
	}
	if matches > 1 {
		err := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(diagnosticName), strings.Join(names(), ", ")), raw.Pos).(*sqlstate.Error)
		err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, err
	}
	e := datetimeExtractExpression{syntax: raw.Syntax}
	if raw.Syntax == parser.SQLFunctionSyntax {
		literal, ok := raw.Args[0].(parser.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("invalid extraction syntax field")
		}
		e.field = literal.Value.Text
	}
	properties := ExprProperties{Volatility: Immutable}
	if selected == sqltype.Timestamptz {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		id := sqltype.Text
		if i == 1 {
			id = selected
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
	// Bound parameters can become constants in PostgreSQL's custom plan. A
	// row/stage-dependent or nonimmutable argument instead permits SQL inlining;
	// that distinction applies to both arguments, not only the DATE source.
	e.dateWrapperContext = name == "date_part" && selected == sqltype.Date && properties.Volatility == Immutable && !properties.RowDependent && !properties.StageDependent
	result := sqltype.Numeric
	if name == "date_part" {
		result = sqltype.Float8
	}
	e.expressionInfo = b.info(b.typ(result).Usage(), raw.Pos, properties)
	plan, err := sqltype.BindDatetimeExtract(name, b.typ(sqltype.Text), b.typ(selected), e.usage.Type, b.typ(sqltype.Timestamp))
	if err != nil {
		return nil, err
	}
	e.plan = plan
	return e, nil
}
