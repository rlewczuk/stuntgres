package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type BlockingInquiry struct {
	expr.Info
	Kind BlockingKind
	Arg  expr.Expr
}

func (BlockingInquiry) ResultNullability(expr.ResultSite) (nullable, known bool) {
	return true, true
}

func bindBlockingInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if typ := arg.Usage.Type; typ != nil {
				name = typ.DiagnosticName()
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := OrdinaryBlockingInquiry
	if name == SafeSnapshotBlockingInquiry.Name() {
		kind = SafeSnapshotBlockingInquiry
	}
	if !visible || name != kind.Name() || len(raw.Args) != 1 || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Int4).Usage()
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if d, ok := failure.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	properties := arg.Properties()
	properties.Volatility = expr.Volatile
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(target.Type.AssociatedArray()).Usage(), raw.Pos, properties), CatalogRoutineProvider, kind.Name(), sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return BlockingInquiry{info, kind, arg}, nil
}

func (n BlockingInquiry) Validate() error {
	if n.Kind.Name() == "" || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() || n.Arg.ResultUsage().Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry argument")
	}
	properties := n.Arg.Properties()
	properties.Volatility = expr.Volatile
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != n.Arg.ResultUsage().Type.AssociatedArray() || n.Usage.Type.Element() != n.Arg.ResultUsage().Type || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry signature")
	}
	return nil
}

func (n BlockingInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	if ctx == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing expression context")
	}
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	pid, err := value.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(BlockingRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	return runtime.BlockingInquiry(BlockingKind(n.Kind), pid, n.Usage.Type)
}
