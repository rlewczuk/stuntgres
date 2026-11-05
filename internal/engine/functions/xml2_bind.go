package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindXML2(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []Argument
	if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	m, selected := XML2Lookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text, len(operands))
	routine, err := b.InstalledRoutine(raw.Name, XML2Extension, m.Signature)
	if err != nil {
		return nil, err
	}
	count := m.Arity
	if !selected || routine == nil || len(raw.ArgNames) != 0 || raw.Variadic || raw.Star {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Text).Usage()
	for _, o := range operands {
		if o.Usage.Type != nil && !b.CanCoerce(o.Usage, target, sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: m.Volatility}
	for i, o := range operands {
		args[i], err = b.CoerceArgument(o, CoercionRequest{Intent: DirectFormalArgument, Target: target, AtRaw: true})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	call := XML2{Info: b.Info(b.Type(m.Result).Usage(), raw.Pos, properties), Name: m.Name, Operation: m.Operation, Args: args}
	if err := call.Validate(); err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
