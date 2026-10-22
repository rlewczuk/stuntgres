package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// LineEquality is the callable spelling of the same released scalar predicate.
// Its routine authority is retained in Info; it grants no default comparator.
type LineEquality struct {
	expr.Info
	Args      [2]expr.Expr
	Predicate sqltype.ComparisonPredicate
}

func bindLineEquality(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
			if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Line).Usage(), sqltype.Implicit); err != nil {
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
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.Line).Usage(), Label: "line_eq"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), OperatorRoutineProvider, "line_eq", sqltype.Line, sqltype.Line)
	if err != nil {
		return nil, err
	}
	return newLineEquality(info, args)
}
func newLineEquality(info expr.Info, args [2]expr.Expr) (LineEquality, error) {
	if missingArgument(args[:]) {
		return LineEquality{}, fmt.Errorf("stuntgres: missing LINE equality operand")
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type, sqltype.CollationRef{}, false)
	return LineEquality{info, args, predicate}, err
}
func (e LineEquality) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
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
func (e LineEquality) Children() []expr.Expr { return e.Args[:] }
func (e LineEquality) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("LineEquality", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(newLineEquality(info, [2]expr.Expr{children[0], children[1]}))
}
func (e LineEquality) ValidateStoredProvider() error {
	n, err := newLineEquality(e.Info, e.Args)
	if err != nil || n.Predicate != e.Predicate {
		return fmt.Errorf("stuntgres: invalid LINE equality provider")
	}
	return nil
}
func (e LineEquality) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e LineEquality) Render() (expr.Rendering, error) {
	spec := callRendering("LineEquality", e.Args[:], "line_eq", e.Args[:])
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e LineEquality) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(LineEquality)
	return ok && e.Predicate == o.Predicate && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e LineEquality) LooksLikeFunction() bool { return true }
func (e LineEquality) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e LineEquality) ClosureTransparent() {}
func (e LineEquality) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}
