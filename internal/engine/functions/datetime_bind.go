package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// datetimeNames are dispatched by exact spelling after the shared call gates.
var datetimeNames = [...]string{
	"extract", "date_part", "date_trunc", "date_bin", "timezone", "date_add", "date_subtract",
	"justify_days", "justify_hours", "justify_interval", "to_char", "to_date", "to_timestamp",
	"age", "isfinite",
}

// datetimeMakeNames accept argument labels; the label gate exempts them.
var datetimeMakeNames = [...]string{"make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval"}

// bindDatetime runs after the shared call gates; builtin visibility follows
// them, as it did in the engine's final name switch.
func bindDatetime(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "age", "isfinite":
		return BindDatetimeAgeFinite(b, raw, visible)
	case "extract", "date_part":
		return bindDatetimeExtract(b, raw, visible)
	case "date_trunc":
		return bindDatetimeTrunc(b, raw, visible)
	case "date_bin":
		return bindDatetimeBin(b, raw, visible)
	case "timezone", "date_add", "date_subtract":
		return bindDatetimeZone(b, raw, visible)
	case "justify_days", "justify_hours", "justify_interval":
		return bindDatetimeJustify(b, raw, visible)
	case "to_char", "to_date", "to_timestamp":
		return bindDatetimeFormatting(b, raw, visible)
	}
	return bindDatetimeMake(b, raw, visible)
}
