package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// bindCurrentDateTime binds a SQL value function. A precision warning is
// returned for the caller to deliver, so a held description's re-analysis
// can stay silent.
func bindCurrentDateTime(b functions.Binder, raw parser.CurrentDateTimeExpr) (expr.Expr, *sqlstate.Error, error) {
	if !functions.CurrentDateTimeShape(raw) {
		return nil, nil, fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	usage, warning, err := functions.ResolveCurrentDateTimeUsage(raw, b.Type(functions.CurrentDateTimeType(raw.Kind)).Usage())
	if err != nil {
		return nil, nil, err
	}
	info := b.Info(usage, raw.Pos, expr.IntrinsicProperties(functions.CurrentDateTimeIntrinsicRule()))
	return functions.CurrentDateTime{Info: info, Kind: raw.Kind, HasPrecision: raw.HasPrecision, Precision: usage.Typmod}, warning, nil
}
