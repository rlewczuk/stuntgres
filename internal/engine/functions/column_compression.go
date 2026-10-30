package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ColumnCompression retains an operand recipe, never physical datum metadata.
type ColumnCompression struct {
	expr.Info
	Arg expr.Expr
}

func bindColumnCompression(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var names []string
	if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: DirectDeferredArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		if names != nil {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pg_column_compression(*) specified, but pg_column_compression is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	child := operand.Expression
	if child == nil {
		child, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
	}
	b.RetainUntypedParameter(child)
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, child.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), CatalogRoutineProvider, "pg_column_compression", 2276)
	if err != nil {
		return nil, err
	}
	return ColumnCompression{Info: info, Arg: child}, nil
}

func (n ColumnCompression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	return sqltype.Value{}, expr.ErrorAt("0A000", "pg_column_compression physical TOAST compression inspection is unsupported in embedded mode", parser.Position{})
}

func (n ColumnCompression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
