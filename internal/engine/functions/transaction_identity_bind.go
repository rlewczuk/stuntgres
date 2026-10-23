package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func identityExpression(b Binder, raw parser.FunctionExpr, kind transactionIdentityKind, arg expr.Expr) (expr.Expr, error) {
	usage := b.Type(sqltype.Int4).Usage()
	properties := expr.Properties{Volatility: expr.Stable}
	if arg != nil {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	switch kind {
	case identityCurrent, identityAssigned:
		usage = b.Type(sqltype.Xid8).Usage()
	case identityStatus:
		usage = b.Type(sqltype.Text).Usage()
		properties.Volatility = expr.Volatile
	case identityMembers:
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "xid", Usage: b.Type(sqltype.Xid).Usage()}, {Attribute: 2, Name: "mode", Usage: b.Type(sqltype.Text).Usage()}})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
		properties.Volatility = expr.Volatile
	}
	formal := [1]sqltype.ID{sqltype.Xid}
	count := 1
	if kind == identityStatus {
		formal[0] = sqltype.Xid8
	}
	if arg == nil {
		count = 0
	}
	provider := CatalogRoutineProvider
	if kind == identityAge {
		provider = DatetimeRoutineProvider
	}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), provider, kind.name(), formal[:count]...)
	if err != nil {
		return nil, err
	}
	return transactionIdentityExpression{info, kind, arg}, nil
}
func bindTransactionIdentity(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := identityMultiAge
	for candidate := identityMultiAge; candidate <= identityMembers; candidate++ {
		if candidate.name() == name {
			kind = candidate
			break
		}
	}
	var operand Argument
	names := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		names[i] = "unknown"
		if arg.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(arg.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	count := 1
	if kind == identityCurrent || kind == identityAssigned {
		count = 0
	}
	formal := sqltype.Xid
	if kind == identityStatus {
		formal = sqltype.Xid8
	}
	valid := visible && name == kind.name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == count
	if len(raw.ArgNames) != 0 {
		valid = valid && kind == identityMembers && transactionMembersNamed(raw)
	}
	if count == 1 && operand.Usage.Type != nil {
		valid = valid && operand.Usage.Type.BaseType() == b.Type(formal)
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	var arg expr.Expr
	if count == 1 {
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formal).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
	}
	return identityExpression(b, raw, kind, arg)
}

func transactionMembersNamed(raw parser.FunctionExpr) bool {
	return len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "multixid"
}

func namedTransactionIdentity(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_get_multixact_members" || raw.Variadic || len(args) != 1 || !transactionMembersNamed(raw) || !declaredTypes(declared, []sqltype.ID{sqltype.Xid}) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, []int{0})
}
