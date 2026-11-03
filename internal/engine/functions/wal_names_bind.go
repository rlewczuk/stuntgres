package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func walNameKind(name string) WALNameKind {
	for i, candidate := range walNameNames {
		if candidate == name {
			return WALNameKind(i)
		}
	}
	return WALNameKind(255)
}
func walNameLabel(k WALNameKind) string {
	if k == WALFileNameOffset {
		return "lsn"
	}
	if k == WALSplitFileName {
		return "file_name"
	}
	return ""
}

func bindWALName(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var one [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = one[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		if i < len(names) {
			typ := "unknown"
			if arg.Usage.Type != nil {
				typ = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, typ)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := walNameKind(name)
	_, named := namedSlots(raw, []string{walNameLabel(kind)}, 1)
	if !visible || kind.Name() == "" || !named || len(raw.Args) != 1 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(kind.input()).Usage()
	if operand.Usage.Type != nil {
		if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: ExactFormalArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	n := WALName{Kind: kind, Arg: arg, Metadata: b.WALNameMetadata()}
	usage := b.Type(sqltype.Text).Usage()
	if kind != WALFileName {
		labels, types := kind.fields()
		members := []sqltype.CompositeMember{
			{Attribute: 1, Name: labels[0], Usage: b.Type(types[0]).Usage()},
			{Attribute: 2, Name: labels[1], Usage: b.Type(types[1]).Usage()},
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: n.IntrinsicVolatility()}, arg.Properties())
	n.Info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, kind.input())
	if err != nil {
		return nil, err
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

func namedWALName(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	kind := walNameKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if kind != WALFileNameOffset && kind != WALSplitFileName || raw.Variadic || len(args) != 1 || len(raw.ArgNames) != 1 || raw.ArgNames[0].Text != walNameLabel(kind) || !declaredTypes(declared, []sqltype.ID{kind.input()}) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, []int{0})
}
