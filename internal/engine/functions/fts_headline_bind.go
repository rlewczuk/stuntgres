package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindFTSHeadline(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	forms := [4][4]sqltype.ID{{sqltype.Text, sqltype.TSQuery}, {sqltype.Text, sqltype.TSQuery, sqltype.Text}, {sqltype.Regconfig, sqltype.Text, sqltype.TSQuery}, {sqltype.Regconfig, sqltype.Text, sqltype.TSQuery, sqltype.Text}}
	operands, selected, err := selectFTSForm(b, raw, forms)
	if err != nil {
		return nil, err
	}
	op := sqltype.FTSHeadlineFunction{Explicit: selected >= 2, Options: selected == 1 || selected == 3}
	volatility := expr.Stable
	if op.Explicit {
		volatility = expr.Immutable
	}
	properties := expr.Properties{Volatility: volatility}
	var args [4]expr.Expr
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(forms[selected][i]).Usage(), Label: "ts_headline"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.CollatedInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), args[:op.Arity()]...)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, FTSHeadlineRoutineProvider, "ts_headline", forms[selected][:op.Arity()]...)
	if err != nil {
		return nil, err
	}
	return NewFTSHeadline(info, op, args)
}
