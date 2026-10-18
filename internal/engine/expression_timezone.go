package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (b *expressionBinder) bindTimeZone(raw parser.TimeZoneExpr) (BoundExpr, error) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "timezone"}}}
	args, pos := []parser.Expr{raw.Zone, raw.Value}, raw.Pos
	if raw.Local {
		args, pos = []parser.Expr{raw.Value}, parser.Position{}
	}
	node, err := bindDatetimeZone(b, parser.FunctionExpr{Name: name, Args: args, Pos: pos}, true)
	if err != nil {
		return nil, err
	}
	e := node.(functions.Timezone)
	e.Function, e.Pos = false, raw.Pos
	// Keep the existing conservative analysis contract distinct from physical
	// plannedVolatility, which uses the actual resolved overload.
	e.Property.Volatility = max(e.Property.Volatility, Stable)
	return e, nil
}
