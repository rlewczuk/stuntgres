package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TextPatternComparison owns strict bytewise TEXT predicates independently of
// expression collation. Info retains genuine scalar routine authority.
type TextPatternComparison struct {
	expr.Info
	Args      [2]expr.Expr
	Name      string
	Predicate sqltype.ComparisonPredicate
}

func textPatternComparisonOperator(name string) (sqltype.ComparisonOperator, bool) {
	switch name {
	case "text_pattern_lt":
		return sqltype.CompareLess, true
	case "text_pattern_le":
		return sqltype.CompareLessEqual, true
	case "text_pattern_gt":
		return sqltype.CompareGreater, true
	case "text_pattern_ge":
		return sqltype.CompareGreaterEqual, true
	}
	return 0, false
}

func BindTextPatternOperator(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	var name string
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "~<~":
		name = "text_pattern_lt"
	case "~<=~":
		name = "text_pattern_le"
	case "~>=~":
		name = "text_pattern_ge"
	case "~>~":
		name = "text_pattern_gt"
	default:
		return nil, fmt.Errorf("stuntgres: invalid text pattern operator")
	}
	return bindTextPatternComparison(b, parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: name}}},
		Args: raw.Args, Pos: raw.Pos,
	})
}

func bindTextPatternComparison(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
			if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
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
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.Text).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), OperatorRoutineProvider, name, sqltype.Text, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return newTextPatternComparison(info, name, args)
}

func newTextPatternComparison(info expr.Info, name string, args [2]expr.Expr) (TextPatternComparison, error) {
	op, ok := textPatternComparisonOperator(name)
	if !ok || missingArgument(args[:]) {
		return TextPatternComparison{}, fmt.Errorf("stuntgres: invalid TextPattern comparison")
	}
	for _, arg := range args {
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Text {
			return TextPatternComparison{}, fmt.Errorf("stuntgres: invalid TextPattern comparison operand")
		}
	}
	predicate, err := sqltype.BindComparisonPredicate(op, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type, sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}, false)
	return TextPatternComparison{Info: info, Args: args, Name: name, Predicate: predicate}, err
}

func (e TextPatternComparison) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e TextPatternComparison) Children() []expr.Expr { return e.Args[:] }
func (e TextPatternComparison) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("TextPatternComparison", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(newTextPatternComparison(info, e.Name, [2]expr.Expr{children[0], children[1]}))
}
func (e TextPatternComparison) ValidateStoredProvider() error {
	n, err := newTextPatternComparison(e.Info, e.Name, e.Args)
	if err != nil || n.Predicate != e.Predicate {
		return fmt.Errorf("stuntgres: invalid TextPattern comparison provider")
	}
	return nil
}
func (e TextPatternComparison) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e TextPatternComparison) Render() (expr.Rendering, error) {
	spec := callRendering("TextPatternComparison", e.Args[:], e.Name, e.Args[:])
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e TextPatternComparison) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(TextPatternComparison)
	return ok && e.Name == o.Name && e.Predicate == o.Predicate && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e TextPatternComparison) LooksLikeFunction() bool { return true }
func (e TextPatternComparison) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e TextPatternComparison) ClosureTransparent() {}
func (e TextPatternComparison) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}
