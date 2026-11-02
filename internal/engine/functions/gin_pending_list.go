package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// GinCleanPendingList retains a typed target recipe, never storage or authority.
type GinCleanPendingList struct {
	expr.Info
	Arg expr.Expr
}

func bindGinCleanPendingList(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:0]
	if len(raw.Args) == 1 {
		names = inline[:]
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
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
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
	if !visible || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Regclass).Usage()
	if operand.Usage.Type != nil && !b.CanCoerce(operand.Usage, target, sqltype.Implicit) {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := b.CaptureTypeInput(target.Type); err != nil {
		return nil, err
	}
	child, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if child.ResultUsage() != target {
		plan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		child, err = b.CoerceArgument(Argument{Expression: child, Raw: raw.Args[0], Usage: child.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, child.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int8).Usage(), raw.Pos, properties), CatalogRoutineProvider, "gin_clean_pending_list", sqltype.Regclass)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return GinCleanPendingList{Info: info, Arg: child}, nil
}

func (n GinCleanPendingList) Eval(ctx expr.Context) (sqltype.Value, error) {
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
	oid, err := value.ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.GinCleanPendingList(oid, n.Usage.Type)
}

func (n GinCleanPendingList) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
