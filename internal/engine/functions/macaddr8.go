package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// MACADDR8Comparison is the callable spelling of the owned scalar predicate.
// Info retains the genuine routine authority; no index provider is implied.
type MACADDR8Comparison struct {
	expr.Info
	Args      [2]expr.Expr
	Name      string
	Predicate sqltype.ComparisonPredicate
}

func macaddr8ComparisonOperator(name string) (sqltype.ComparisonOperator, bool) {
	switch name {
	case "macaddr8_eq":
		return sqltype.CompareEqual, true
	case "macaddr8_ne":
		return sqltype.CompareNotEqual, true
	case "macaddr8_lt":
		return sqltype.CompareLess, true
	case "macaddr8_le":
		return sqltype.CompareLessEqual, true
	case "macaddr8_gt":
		return sqltype.CompareGreater, true
	case "macaddr8_ge":
		return sqltype.CompareGreaterEqual, true
	}
	return 0, false
}

func bindMACADDR8Comparison(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operands [2]Argument
	valid := len(raw.Args) == 2 && !raw.Variadic && len(raw.ArgNames) == 0
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = arg
		}
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Macaddr8).Usage(), sqltype.Implicit); err != nil {
				valid = false
			}
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !valid || !visible {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.Macaddr8).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), OperatorRoutineProvider, name, sqltype.Macaddr8, sqltype.Macaddr8)
	if err != nil {
		return nil, err
	}
	return newMACADDR8Comparison(info, name, args)
}

func newMACADDR8Comparison(info expr.Info, name string, args [2]expr.Expr) (MACADDR8Comparison, error) {
	op, ok := macaddr8ComparisonOperator(name)
	if !ok || missingArgument(args[:]) {
		return MACADDR8Comparison{}, fmt.Errorf("stuntgres: invalid MACADDR8 comparison")
	}
	for _, arg := range args {
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Macaddr8 {
			return MACADDR8Comparison{}, fmt.Errorf("stuntgres: invalid MACADDR8 comparison operand")
		}
	}
	predicate, err := sqltype.BindComparisonPredicate(op, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type, sqltype.CollationRef{}, false)
	return MACADDR8Comparison{Info: info, Args: args, Name: name, Predicate: predicate}, err
}

func (e MACADDR8Comparison) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	a, err := e.Args[0].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Args[1].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Predicate.Evaluate(ctx, a, b, sqltype.InputEnvironment{})
}
func (e MACADDR8Comparison) Children() []expr.Expr { return e.Args[:] }
func (e MACADDR8Comparison) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("MACADDR8Comparison", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(newMACADDR8Comparison(info, e.Name, [2]expr.Expr{children[0], children[1]}))
}
func (e MACADDR8Comparison) ValidateStoredProvider() error {
	n, err := newMACADDR8Comparison(e.Info, e.Name, e.Args)
	if err != nil || n.Predicate != e.Predicate {
		return fmt.Errorf("stuntgres: invalid MACADDR8 comparison provider")
	}
	return nil
}
func (e MACADDR8Comparison) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e MACADDR8Comparison) Render() (expr.Rendering, error) {
	spec := callRendering("MACADDR8Comparison", e.Args[:], e.Name, e.Args[:])
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e MACADDR8Comparison) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(MACADDR8Comparison)
	return ok && e.Name == o.Name && e.Predicate == o.Predicate && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e MACADDR8Comparison) LooksLikeFunction() bool { return true }
func (e MACADDR8Comparison) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e MACADDR8Comparison) ClosureTransparent() {}
func (e MACADDR8Comparison) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}
