package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindCommitTimestamp(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := commitTimestampScalar
	if name == "pg_xact_commit_timestamp_origin" {
		kind = commitTimestampOrigin
	}
	if name == "pg_last_committed_xact" {
		kind = commitTimestampLast
	}
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
		if names != nil {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	count := 1
	if kind == commitTimestampLast {
		count = 0
	}
	valid := visible && name == kind.name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == count
	if len(raw.ArgNames) != 0 {
		valid = valid && kind == commitTimestampOrigin && len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "xid"
	}
	if count == 1 && operand.Usage.Type != nil {
		valid = valid && operand.Usage.Type.BaseType() == b.Type(sqltype.Xid)
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
	properties := expr.Properties{Volatility: expr.Volatile}
	if count == 1 {
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.Xid).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	usage := b.Type(sqltype.Timestamptz).Usage()
	if kind != commitTimestampScalar {
		members := make([]sqltype.CompositeMember, 0, 3)
		if kind == commitTimestampLast {
			members = append(members, sqltype.CompositeMember{Attribute: 1, Name: "xid", Usage: b.Type(sqltype.Xid).Usage()})
		}
		members = append(members, sqltype.CompositeMember{Attribute: int32(len(members) + 1), Name: "timestamp", Usage: usage})
		members = append(members, sqltype.CompositeMember{Attribute: int32(len(members) + 1), Name: "roident", Usage: b.Type(sqltype.Oid).Usage()})
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	formal := [1]sqltype.ID{sqltype.Xid}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	return commitTimestampExpression{info, kind, arg}, nil
}
