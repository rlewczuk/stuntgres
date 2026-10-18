package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// bindTransactionTime binds now, transaction_timestamp, statement_timestamp,
// clock_timestamp and timeofday. They sat before the shared gates and own
// their label and FILTER diagnostics.
func bindTransactionTime(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	kind := TransactionTimeKindForName(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, err = ArgumentTypeNames(b, raw)
		if err != nil {
			return nil, err
		}
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !TransactionTimeAllowed(raw, visible, kind) {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	if decoration := TransactionTimeDecoration(raw); decoration != "" {
		if decoration == "(*)" {
			return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
		}
		owner := "an aggregate function"
		if decoration == "OVER" {
			owner = "a window function nor an aggregate function"
		}
		return nil, expr.ErrorAt("42809", decoration+" specified, but "+name+" is not "+owner, raw.Pos)
	}
	info := b.Info(b.Type(kind.Type()).Usage(), raw.Pos, expr.IntrinsicProperties(kind.IntrinsicRule()))
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return TransactionTime{Info: info, Kind: kind}, nil
}
