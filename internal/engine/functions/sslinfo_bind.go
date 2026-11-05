package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindSSLInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var single [1]Argument
	var operands []Argument
	if len(raw.Args) == 1 {
		operands = single[:]
	} else if len(raw.Args) > 1 && len(raw.Args) <= 100 {
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
	member, selected := SSLInfoLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	routine, err := b.InstalledRoutine(raw.Name, SSLInfoExtension, member.Signature)
	if err != nil {
		return nil, err
	}
	arity := 0
	if member.Signature != "" {
		arity = 1
	}
	valid := selected && routine != nil && len(raw.Args) == arity && len(raw.ArgNames) == 0 && !raw.Variadic && !raw.Star
	formal := b.Type(sqltype.Text).Usage()
	if valid && arity == 1 && operands[0].Usage.Type != nil {
		_, err = sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit)
		valid = err == nil
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.Type(member.Result).Usage()
	if member.Set {
		members := []sqltype.CompositeMember{
			{Attribute: 1, Name: "name", Usage: formal},
			{Attribute: 2, Name: "value", Usage: formal},
			{Attribute: 3, Name: "critical", Usage: b.Type(sqltype.Bool).Usage()},
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	properties := expr.Properties{Volatility: expr.Volatile}
	var arg expr.Expr
	if arity == 1 {
		arg, err = b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: formal})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	call := SSLInfo{Info: b.Info(usage, raw.Pos, properties), Name: member.Name, Arg: arg}
	if err := call.Validate(); err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
