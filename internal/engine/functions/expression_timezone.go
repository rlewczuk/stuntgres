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

// Timezone implements expr.Composite. Children follow evaluation order, Zone
// before Value, as in PostgreSQL's timezone(zone, value); AT LOCAL has no Zone
// child.
var _ expr.Composite = Timezone{}

func (e Timezone) Children() []expr.Expr {
	if e.Local {
		return []expr.Expr{e.Value}
	}
	return []expr.Expr{e.Zone, e.Value}
}
func (e Timezone) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if e.Local {
		if err := checkChildren("Timezone", children, 1); err != nil {
			return nil, err
		}
		e.Info, e.Value = info, children[0]
		return e, nil
	}
	if err := checkChildren("Timezone", children, 2); err != nil {
		return nil, err
	}
	e.Info, e.Zone, e.Value = info, children[0], children[1]
	return e, nil
}

func (e Timezone) IntrinsicVolatility() expr.Volatility { return stableIf(e.IntrinsicStable()) }

func (e Timezone) StrictNull() expr.Strict { return expr.StrictBefore }

func (e Timezone) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = Timezone{}

// Render implements expr.Renderable.
func (e Timezone) Render() (expr.Rendering, error) {
	if e.Function {
		return callRendering("Datetime timezone", e.Arguments(), "timezone", e.Arguments()), nil
	}
	text, arg := expr.TextPiece, expr.ArgPiece
	if e.Local {
		return expr.Rendering{Tree: "AtLocal", Inputs: []expr.Expr{e.Value}, Form: expr.FormTemplate, Args: hiddenArgs(e.Value), Pieces: []expr.Piece{text("("), arg(0), text(" AT LOCAL)")}}, nil
	}
	return expr.Rendering{Tree: "AtTimeZone", Inputs: []expr.Expr{e.Value, e.Zone}, Form: expr.FormTemplate, Args: hiddenArgs(e.Value, e.Zone), Pieces: []expr.Piece{text("("), arg(0), text(" AT TIME ZONE "), arg(1), text(")")}}, nil
}

// SameGroup implements expr.GroupIdentity.
func (e Timezone) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Timezone)
	return ok && e.Local == o.Local && same(e.Value, o.Value) && (e.Local || same(e.Zone, o.Zone))
}

// PreflightFold implements expr.Preflighting.
func (e Timezone) PreflightFold() expr.Preflight { return expr.PreflightNullFirst }
