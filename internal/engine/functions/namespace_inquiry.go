package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type NamespaceInquiryKind uint8

const (
	MyTempNamespace NamespaceInquiryKind = iota
	OtherTempNamespace
)

func (k NamespaceInquiryKind) name() string {
	if k == MyTempNamespace {
		return "pg_my_temp_schema"
	}
	if k == OtherTempNamespace {
		return "pg_is_other_temp_schema"
	}
	return ""
}

type NamespaceInquiry struct {
	expr.Info
	Kind NamespaceInquiryKind
	Arg  expr.Expr
}

func (e NamespaceInquiry) ResultNullability(expr.ResultSite) (nullable, known bool) {
	return e.Kind == OtherTempNamespace, true
}

func bindNamespaceInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) != 0 && len(raw.Args) <= 100 {
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
			names[i] = "unknown"
			if typ := arg.Usage.Type; typ != nil {
				names[i] = typ.DiagnosticName()
			}
			names[i] = NamedArgumentType(raw, i, names[i])
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
	kind, arity, result := MyTempNamespace, 0, b.Type(sqltype.Oid).Usage()
	if name == "pg_is_other_temp_schema" {
		kind, arity, result = OtherTempNamespace, 1, b.Type(sqltype.Bool).Usage()
	}
	if !visible || name != kind.name() || len(raw.Args) != arity || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	properties := expr.Properties{Volatility: expr.Stable}
	var arg expr.Expr
	if arity == 1 {
		target := b.Type(sqltype.Oid).Usage()
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
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
		properties = arg.Properties()
		properties.Volatility = max(expr.Stable, properties.Volatility)
	}
	return NamespaceInquiry{b.Info(result, raw.Pos, properties), kind, arg}, nil
}

func (n NamespaceInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var oid uint32
	if n.Kind == OtherTempNamespace {
		value, err := n.Arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		oid, err = value.OID()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	runtime, err := sessionInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == MyTempNamespace {
		value, err := runtime.MyTempNamespace()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.OIDValue(n.Usage.Type, value)
	}
	if n.Kind != OtherTempNamespace {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid namespace inquiry")
	}
	value, err := runtime.OtherTempNamespace(oid)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(n.Usage.Type, value), nil
}
func (n NamespaceInquiry) arguments() []expr.Expr {
	if n.Arg == nil {
		return nil
	}
	return []expr.Expr{n.Arg}
}

func (n NamespaceInquiry) Validate() error {
	result := sqltype.Oid
	properties := expr.Properties{Volatility: expr.Stable}
	if n.Kind == OtherTempNamespace {
		result = sqltype.Bool
		if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() || n.Arg.ResultUsage().Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid stored namespace inquiry argument")
		}
		properties = n.Arg.Properties()
		properties.Volatility = max(expr.Stable, properties.Volatility)
	} else if n.Kind != MyTempNamespace || n.Arg != nil {
		return fmt.Errorf("stuntgres: invalid stored namespace inquiry kind")
	}
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != result || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored namespace inquiry signature")
	}
	return nil
}

func (n NamespaceInquiry) Children() []expr.Expr { return n.arguments() }
func (n NamespaceInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 0
	if n.Arg != nil {
		count = 1
	}
	if err := checkChildren("namespace inquiry", children, count); err != nil {
		return nil, err
	}
	n.Info = info
	if count == 1 {
		n.Arg = children[0]
	}
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n NamespaceInquiry) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (NamespaceInquiry) PlanningFold() expr.Fold                { return expr.FoldNever }
func (NamespaceInquiry) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (NamespaceInquiry) LooksLikeFunction() bool                { return true }
func (n NamespaceInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(NamespaceInquiry)
	return ok && n.Kind == o.Kind && (n.Arg == nil && o.Arg == nil || n.Arg != nil && o.Arg != nil && same(n.Arg, o.Arg))
}
func (n NamespaceInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := expr.Rendering{Tree: fmt.Sprintf("NamespaceInquiry %d", n.Kind), Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.name()}
	if n.Arg != nil {
		spec.Args = []expr.RenderArg{{Expr: n.Arg}}
	}
	return spec, nil
}
func (n NamespaceInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	operands := expr.CaptureOperands{}
	if n.Arg != nil {
		operands.Count = 1
		operands.Inline[0] = n.Arg
	}
	return operands, true, nil
}
