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
	properties := value.Properties()
	properties.Volatility = Stable
	var zone BoundExpr
	if !raw.Local {
		zone, err = b.bind(raw.Zone, expressionPolicy{context: policy.context, operand: true, expected: b.typ(sqltype.Text).Usage(), mode: sqltype.Implicit})
		if err != nil {
			return nil, err
		}
		zp := zone.Properties()
		properties.RowDependent = properties.RowDependent || zp.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || zp.ParameterDependent
		properties.StageDependent = properties.StageDependent || zp.StageDependent
		if zp.Volatility > properties.Volatility {
			properties.Volatility = zp.Volatility
		}
	}
	return timeZoneExpression{expressionInfo: b.info(b.typ(result).Usage(), raw.Pos, properties), value: value, zone: zone, local: raw.Local}, nil
}
