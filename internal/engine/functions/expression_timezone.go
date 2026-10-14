package functions

import (
	"stuntgres/internal/sqltype"
)

type TimezoneContext[E any] interface {
	LiteralContext[E]
	FunctionInput() sqltype.InputEnvironment
}

type Timezone[E Expression[C], C TimezoneContext[E], P any] struct {
	Info[P]
	Value, Zone E
	Local       bool
	Function    bool
}

func (e Timezone[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func (e Timezone[E, C, P]) IntrinsicStable() bool {
	return e.Local || e.Value.ResultUsage().Type.BaseType().ID() == sqltype.Timetz && e.Zone.ResultUsage().Type.BaseType().ID() != sqltype.Interval
}
func (e Timezone[E, C, P]) Arguments() []E {
	if e.Local {
		return []E{e.Value}
	}
	return []E{e.Zone, e.Value}
}
