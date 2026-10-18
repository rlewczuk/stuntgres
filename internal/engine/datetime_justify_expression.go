package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDatetimeJustify(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [1]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o.Usage.Type)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	target := b.typ(sqltype.Interval)
	selection := functions.ResolveDatetimeJustify(visible, raw.Variadic, operands, target.Usage())
	if selection.Resolution == functions.Missing {
		return signature()
	}
	if selection.Unsupported != "" {
		return nil, expressionError("0A000", selection.Unsupported, raw.Pos)
	}
	o := operands[0]
	arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: target.Usage()})
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.BindDatetimeJustify(raw.Name.Parts[len(raw.Name.Parts)-1].Text, target, target)
	if err != nil {
		return nil, err
	}
	info, err := b.builtinRoutineInfo(b.info(target.Usage(), raw.Pos, arg.Properties()), datetimeRoutineProvider, raw.Name.Parts[len(raw.Name.Parts)-1].Text, sqltype.Interval)
	if err != nil {
		return nil, err
	}
	return functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Arg: arg}, nil
}
