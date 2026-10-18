package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindDatetimeJustify(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	var small [1]Argument
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = DiagnosticNameOrUnknown(o.Usage.Type)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Interval)
	selection := ResolveDatetimeJustify(visible, raw.Variadic, operands, target.Usage())
	if selection.Resolution == Missing {
		return signature()
	}
	if selection.Unsupported != "" {
		return nil, expr.ErrorAt("0A000", selection.Unsupported, raw.Pos)
	}
	o := operands[0]
	arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target.Usage()})
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.BindDatetimeJustify(raw.Name.Parts[len(raw.Name.Parts)-1].Text, target, target)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(target.Usage(), raw.Pos, arg.Properties()), DatetimeRoutineProvider, raw.Name.Parts[len(raw.Name.Parts)-1].Text, sqltype.Interval)
	if err != nil {
		return nil, err
	}
	return DatetimeJustify{Info: info, Plan: plan, Arg: arg}, nil
}
