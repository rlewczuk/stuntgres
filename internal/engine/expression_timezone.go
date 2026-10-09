package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type timeZoneExpression struct {
	expressionInfo
	value, zone BoundExpr
	local       bool
}

func (e timeZoneExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	value, err := e.value.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.usage.Type), nil
	}
	name := ""
	if !e.local {
		zone, err := e.zone.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if zone.IsNull() {
			return sqltype.Null(e.usage.Type), nil
		}
		if zone.Type().ID() == sqltype.Interval {
			return sqltype.AtTimeZoneInterval(value, e.usage.Type, zone, c.Input)
		}
		name, err = zone.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.AtTimeZone(value, e.usage.Type, name, e.local, c.Input)
}
func (b *expressionBinder) bindTimeZone(raw parser.TimeZoneExpr, policy expressionPolicy) (BoundExpr, error) {
	value, err := b.bind(raw.Value, expressionPolicy{context: policy.context, operand: true})
	if err != nil {
		return nil, err
	}
	if value.ResultUsage().Type.IsDomain() {
		value, err = b.coerceConditional(comparisonOperand{node: value}, value.ResultUsage().Type.BaseType().Usage(), "AT TIME ZONE")
		if err != nil {
			return nil, err
		}
	}
	id := value.ResultUsage().Type.ID()
	var result sqltype.ID
	switch id {
	case sqltype.Timestamp:
		result = sqltype.Timestamptz
	case sqltype.Timestamptz:
		result = sqltype.Timestamp
	case sqltype.Timetz:
		result = sqltype.Timetz
	default:
		return nil, expressionError("42883", "function pg_catalog.timezone(text, "+value.ResultUsage().Type.DiagnosticName()+") does not exist", raw.Pos)
	}
	properties := mergeExpressionProperties(value.Properties(), ExprProperties{Volatility: Stable})
	var zone BoundExpr
	if !raw.Local {
		zone, err = b.bind(raw.Zone, expressionPolicy{context: policy.context, operand: true, expected: b.typ(sqltype.Text).Usage(), mode: sqltype.Implicit})
		if err != nil {
			return nil, err
		}
		if zone.ResultUsage().Type.IsDomain() {
			zone, err = b.coerceConditional(comparisonOperand{node: zone}, zone.ResultUsage().Type.BaseType().Usage(), "AT TIME ZONE")
			if err != nil {
				return nil, err
			}
		}
		properties = mergeExpressionProperties(properties, zone.Properties())
	}
	return timeZoneExpression{expressionInfo: b.info(b.typ(result).Usage(), raw.Pos, properties), value: value, zone: zone, local: raw.Local}, nil
}
