package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// UnknownInputCast is immutable analysis provenance on an existing coercion.
// Source is the exact nominal Usage, not the physical domain base codec.
type UnknownInputCast struct {
	Source      sqltype.Usage
	Target      *sqltype.Descriptor
	Syntax      parser.CastExpr
	Transparent []expr.TransparentInputCast
}

func (e CoercionExpr) LogicalInputCast() (expr.InputCast, error) {
	if e.UnknownInputCast == nil {
		if e.Plan.IsUnknownInputTransport() {
			return expr.InputCast{}, fmt.Errorf("stuntgres: unknown input transport lacks logical authority")
		}
		return expr.InputCast{}, nil
	}
	m := e.UnknownInputCast
	if e.Child == nil || m.Target == nil || m.Target.ID() != sqltype.Unknown || e.Mode != sqltype.Explicit || e.SyntheticConstructorElement || e.Child.ResultUsage() != m.Source || !e.Plan.IsUnknownInputTransport() {
		return expr.InputCast{}, fmt.Errorf("stuntgres: invalid logical unknown input cast")
	}
	if err := expr.ValidateTransparentInputCasts(m.Syntax, m.Transparent); err != nil {
		return expr.InputCast{}, err
	}
	p, err := sqltype.PlanUnknownInputTransport(m.Source, e.Usage)
	if err != nil || !p.Equal(e.Plan) {
		return expr.InputCast{}, fmt.Errorf("stuntgres: invalid unknown input IO plan")
	}
	properties := e.Child.Properties()
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.ChecksDomain()
	if p.SessionDependent() && properties.Volatility < expr.Stable {
		properties.Volatility = expr.Stable
	}
	if e.Properties() != properties {
		return expr.InputCast{}, fmt.Errorf("stuntgres: invalid unknown input cast properties")
	}
	return expr.InputCast{Source: e.Child, Target: m.Target.Usage(), Syntax: m.Syntax, Transparent: m.Transparent}, nil
}
