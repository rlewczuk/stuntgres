package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// BindCurrentDateTime binds a SQL value function; the engine dispatches it by
// syntax, not by name. A precision warning is
// returned for the caller to deliver, so a held description's re-analysis
// can stay silent.
func BindCurrentDateTime(b Binder, raw parser.CurrentDateTimeExpr) (expr.Expr, *sqlstate.Error, error) {
	if !CurrentDateTimeShape(raw) {
		return nil, nil, fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	usage, warning, err := ResolveCurrentDateTimeUsage(raw, b.Type(CurrentDateTimeType(raw.Kind)).Usage())
	if err != nil {
		return nil, nil, err
	}
	info := b.Info(usage, raw.Pos, expr.IntrinsicProperties(CurrentDateTimeIntrinsicRule()))
	return CurrentDateTime{Info: info, Kind: raw.Kind, HasPrecision: raw.HasPrecision, Precision: usage.Typmod}, warning, nil
}
