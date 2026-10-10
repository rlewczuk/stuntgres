package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type timeZoneExpression struct {
	expressionInfo
	value, zone BoundExpr
	local       bool
	function    bool
}

// Explicit timestamp and interval-zone timetz overloads are immutable; local
// zones and text-zone timetz depend on the captured session/transaction clock.
func (e timeZoneExpression) intrinsicVolatility() Volatility {
	if e.local || e.value.ResultUsage().Type.BaseType().ID() == sqltype.Timetz && e.zone.ResultUsage().Type.BaseType().ID() != sqltype.Interval {
		return Stable
	}
	return Immutable
}

func (e timeZoneExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if randomLiteralNull(e.value) || !e.local && randomLiteralNull(e.zone) {
		return sqltype.Null(e.usage.Type), nil
	}
	var zone sqltype.Value
	var err error
	// PostgreSQL lowers AT into timezone(zone,value), so function and operator
	// share argument demand. Runtime NULLs suppress the body, not sibling work.
	if !e.local {
		zone, err = e.zone.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	value, err := e.value.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() || !e.local && zone.IsNull() {
		return sqltype.Null(e.usage.Type), nil
	}
	if !e.local && zone.Type().ID() == sqltype.Interval {
		return sqltype.AtTimeZoneInterval(value, e.usage.Type, zone, c.Input)
	}
	name := ""
	if !e.local {
		name, err = zone.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.AtTimeZone(value, e.usage.Type, name, e.local, c.Input)
}

func (b *expressionBinder) bindTimeZone(raw parser.TimeZoneExpr) (BoundExpr, error) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "timezone"}}}
	args, pos := []parser.Expr{raw.Zone, raw.Value}, raw.Pos
	if raw.Local {
		args, pos = []parser.Expr{raw.Value}, parser.Position{}
	}
	node, err := b.bindDatetimeZone(parser.FunctionExpr{Name: name, Args: args, Pos: pos}, true)
	if err != nil {
		return nil, err
	}
	e := node.(timeZoneExpression)
	e.function, e.pos = false, raw.Pos
	// Keep the existing conservative analysis contract distinct from physical
	// plannedVolatility, which uses the actual resolved overload.
	e.properties.Volatility = max(e.properties.Volatility, Stable)
	return e, nil
}

func (e timeZoneExpression) functionArguments() []BoundExpr {
	if e.local {
		return []BoundExpr{e.value}
	}
	return []BoundExpr{e.zone, e.value}
}
