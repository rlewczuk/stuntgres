package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// MACUnary keeps the source descriptor separate from the SQL routine's formal
// type: set7bit(macaddr) expands locally while still selecting routine 4125.
type MACUnary struct {
	expr.Info
	Arg      expr.Expr
	Name     string
	Provider sqltype.MACUnary
}

func bindMACSet7Bit(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	valid := len(raw.Args) == 1 && !raw.Variadic
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		if arg.Usage.Type != nil {
			id := arg.Usage.Type.BaseType().ID()
			valid = valid && (id == sqltype.Macaddr || id == sqltype.Macaddr8)
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
	target := b.Type(sqltype.Macaddr8)
	if operand.Usage.Type != nil && operand.Usage.Type.BaseType().ID() == sqltype.Macaddr {
		target = b.Type(sqltype.Macaddr)
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: target.Usage(), Label: "macaddr8_set7bit"})
	if err != nil {
		return nil, err
	}
	return bindMACUnary(b, raw, "macaddr8_set7bit", arg)
}

// bindMACUnary consumes an already analyzed/coerced child. The existing math
// registry owner delegates only an exact one-argument typed MAC trunc here.
func bindMACUnary(b Binder, raw parser.FunctionExpr, name string, arg expr.Expr) (expr.Expr, error) {
	result := arg.ResultUsage().Type
	formal := result.ID()
	if name == "macaddr8_set7bit" {
		result, formal = b.Type(sqltype.Macaddr8), sqltype.Macaddr8
	}
	info, err := b.BuiltinRoutineInfo(b.Info(result.Usage(), raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())), NetworkRoutineProvider, name, formal)
	if err != nil {
		return nil, err
	}
	return newMACUnary(info, name, arg)
}

func newMACUnary(info expr.Info, name string, arg expr.Expr) (MACUnary, error) {
	if arg == nil || info.Usage.Type == nil || info.Usage != info.Usage.Type.Usage() || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() {
		return MACUnary{}, fmt.Errorf("stuntgres: invalid MAC function operands")
	}
	kind := sqltype.MACTrunc
	switch name {
	case "trunc":
	case "macaddr8_set7bit":
		kind = sqltype.MACSet7Bit
	default:
		return MACUnary{}, fmt.Errorf("stuntgres: invalid MAC function name")
	}
	provider, err := sqltype.BindMACUnary(kind, arg.ResultUsage().Type, info.Usage.Type)
	return MACUnary{Info: info, Arg: arg, Name: name, Provider: provider}, err
}

func (e MACUnary) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	v, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Evaluate(ctx, v)
}
func (e MACUnary) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e MACUnary) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("MACUnary", children, 1); err != nil {
		return nil, err
	}
	return rebuilt(newMACUnary(info, e.Name, children[0]))
}
func (e MACUnary) ValidateStoredProvider() error {
	n, err := newMACUnary(e.Info, e.Name, e.Arg)
	if err != nil || n.Provider != e.Provider {
		return fmt.Errorf("stuntgres: invalid MAC function provider")
	}
	// This checks the selected identity only; the stored renderer separately
	// validates the captured catalog declaration and its owning lifetime.
	oid := uint32(4125)
	if e.Name == "trunc" {
		oid = 753
		if e.Usage.Type.ID() == sqltype.Macaddr8 {
			oid = 4112
		}
	}
	if e.RoutineReference == nil || e.RoutineReference.RoutineOID() != oid {
		return fmt.Errorf("stuntgres: invalid MAC function routine identity")
	}
	return nil
}
func (e MACUnary) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e MACUnary) Render() (expr.Rendering, error) {
	args := e.Children()
	spec := callRendering("MACUnary", args, e.Name, args)
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e MACUnary) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(MACUnary)
	return ok && e.Name == o.Name && e.Provider == o.Provider && same(e.Arg, o.Arg)
}
func (e MACUnary) LooksLikeFunction() bool { return true }
func (e MACUnary) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e MACUnary) ClosureTransparent() {}
func (e MACUnary) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: [2]expr.Expr{e.Arg}, Count: 1}, true, nil
}
