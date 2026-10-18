package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type Timezone struct {
	expr.Info
	Value, Zone expr.Expr
	Local       bool
	Function    bool
}

func (e Timezone) Eval(c expr.Context) (sqltype.Value, error) {
	_, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Value) || !e.Local && c.FunctionLiteralNull(e.Zone) {
		return sqltype.Null(e.Usage.Type), nil
	}
	var zone sqltype.Value
	err = nil
	// PostgreSQL lowers AT into timezone(zone,value), so function and operator
	// share argument demand. Runtime NULLs suppress the body, not sibling work.
	if !e.Local {
		zone, err = e.Zone.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	value, err := e.Value.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() || !e.Local && zone.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	if !e.Local && zone.Type().ID() == sqltype.Interval {
		return sqltype.AtTimeZoneInterval(value, e.Usage.Type, zone, c.FunctionInput())
	}
	name := ""
	if !e.Local {
		name, err = zone.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.AtTimeZone(value, e.Usage.Type, name, e.Local, c.FunctionInput())
}

func (e Timezone) IntrinsicStable() bool {
	return e.Local || e.Value.ResultUsage().Type.BaseType().ID() == sqltype.Timetz && e.Zone.ResultUsage().Type.BaseType().ID() != sqltype.Interval
}
func (e Timezone) Arguments() []expr.Expr {
	if e.Local {
		return []expr.Expr{e.Value}
	}
	return []expr.Expr{e.Zone, e.Value}
}

// Timezone implements expr.Composite. Children lists Value before Zone, the
// order every engine traversal used; AT LOCAL has no Zone child.
var _ expr.Composite = Timezone{}

func (e Timezone) Children() []expr.Expr {
	if e.Local {
		return []expr.Expr{e.Value}
	}
	return []expr.Expr{e.Value, e.Zone}
}
func (e Timezone) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	want := 2
	if e.Local {
		want = 1
	}
	if err := checkChildren("Timezone", children, want); err != nil {
		return nil, err
	}
	e.Info, e.Value = info, children[0]
	if !e.Local {
		e.Zone = children[1]
	}
	return e, nil
}

func (e Timezone) IntrinsicVolatility() expr.Volatility { return stableIf(e.IntrinsicStable()) }
