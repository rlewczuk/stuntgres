package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Posix struct {
	expr.Info
	Left, Right         expr.Expr
	Provider            sqltype.PosixPlan
	Operator, Namespace string
}
type PosixSubstring struct {
	expr.Info
	Args     [2]expr.Expr
	Provider sqltype.PosixSubstringPlan
	Syntax   parser.FunctionSyntax
}

func (e Posix) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	a, err := e.Left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() {
		c.FunctionRegexExecution().Reset()
		return sqltype.Null(e.Usage.Type), nil
	}
	b, err := e.Right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Evaluate(ctx, a, b, c.FunctionRegexExecution())
}
func (e PosixSubstring) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			c.FunctionRegexExecution().Reset()
			return sqltype.Null(e.Usage.Type), nil
		}
		values[i] = value
	}
	return e.Provider.Evaluate(ctx, values, c.FunctionRegexExecution())
}
func PosixName(name string) bool { return name == "~" || name == "~*" || name == "!~" || name == "!~*" }
func PosixShape(raw parser.OperatorExpr) bool {
	return len(raw.Args) == 2 && len(raw.Name.Parts) >= 1 && len(raw.Name.Parts) <= 2
}
func PatternTextType(d *sqltype.Descriptor) bool {
	if d == nil {
		return true
	}
	switch d.BaseType().ID() {
	case sqltype.Text, sqltype.Name, sqltype.BPChar, sqltype.VarChar:
		return true
	}
	return false
}
func PatternAllowed(visible bool, left, right sqltype.Usage) bool {
	return visible && PatternTextType(left.Type) && PatternTextType(right.Type)
}
func CoercePatternArguments(b ArgumentCoercer, left, right Argument, text sqltype.Usage) (l, r expr.Expr, err error) {
	target := text
	if d := left.Usage.Type; d != nil && (d.BaseType().ID() == sqltype.Name || d.BaseType().ID() == sqltype.BPChar) {
		target = d.BaseType().Usage()
	}
	l, err = b.CoerceArgument(left, CoercionRequest{Intent: LikeArgument, Target: target})
	if err != nil {
		return l, r, err
	}
	r, err = b.CoerceArgument(right, CoercionRequest{Intent: LikeArgument, Target: text})
	return l, r, err
}
func CoercePosixSubstringArguments(b ArgumentCoercer, operands []Argument, text sqltype.Usage) (args [2]expr.Expr, err error) {
	for i, operand := range operands {
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: LikeArgument, Target: text})
		if err != nil {
			return args, err
		}
	}
	return args, nil
}
func NewPosix(info expr.Info, left, right expr.Expr, operator, namespace string, collation sqltype.CollationRef, conflict bool) (Posix, error) {
	provider, err := sqltype.BindPosix(operator, left.ResultUsage(), right.ResultUsage(), info.Usage, collation, conflict)
	return Posix{info, left, right, provider, operator, namespace}, err
}
func (e Posix) Reconstruct(info expr.Info, left, right expr.Expr, collation sqltype.CollationRef, conflict bool) (Posix, error) {
	return NewPosix(info, left, right, e.Operator, e.Namespace, collation, conflict)
}
func NewPosixSubstring(info expr.Info, args [2]expr.Expr, syntax parser.FunctionSyntax) (PosixSubstring, error) {
	usages := [2]sqltype.Usage{args[0].ResultUsage(), args[1].ResultUsage()}
	provider, err := sqltype.BindPosixSubstring(usages, info.Usage, info.Usage.Collation, info.CollationConflict)
	return PosixSubstring{info, args, provider, syntax}, err
}
func (e PosixSubstring) Reconstruct(info expr.Info, args [2]expr.Expr) (PosixSubstring, error) {
	return NewPosixSubstring(info, args, e.Syntax)
}

// PosixSubstring implements expr.Composite.
var _ expr.Composite = PosixSubstring{}

func (e PosixSubstring) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e PosixSubstring) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("PosixSubstring", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

// Posix implements expr.Composite. Rebuild keeps the bound input collation.
var _ expr.Composite = Posix{}

func (e Posix) Children() []expr.Expr { return []expr.Expr{e.Left, e.Right} }
func (e Posix) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Posix", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children[0], children[1], e.Provider.Collation(), e.Provider.Conflict()))
}

func (e Posix) IntrinsicVolatility() expr.Volatility          { return expr.Immutable }
func (e PosixSubstring) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e Posix) ValidateStoredProvider() error          { return nil }
func (e PosixSubstring) ValidateStoredProvider() error { return nil }

// Retag recomputes the input collation from the retagged operands.
func (e Posix) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if !changed {
		return e, false, nil
	}
	collation, err := env.Collated(expr.Info{Usage: children[0].ResultUsage(), Pos: info.Pos}, children...)
	if err != nil {
		return nil, false, err
	}
	node, err := e.Reconstruct(info, children[0], children[1], collation.Usage.Collation, collation.CollationConflict)
	return retagged(node, true, err)
}

// Retag derives the result collation from the retagged operands.
func (e PosixSubstring) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if !changed {
		return e, false, nil
	}
	info, err := env.Collated(info, children...)
	if err != nil {
		return nil, false, err
	}
	args := e.Args
	copy(args[:], children)
	node, err := e.Reconstruct(info, args)
	return retagged(node, true, err)
}

var _ expr.Renderable = Posix{}

// Render implements expr.Renderable.
func (e Posix) Render() (expr.Rendering, error) {
	return infixRendering(fmt.Sprintf("Posix %s namespace %q", e.Operator, e.Namespace), []expr.Expr{e.Left, e.Right}, e.Namespace, e.Operator, e.Left, e.Right), nil
}

var _ expr.Renderable = PosixSubstring{}

// Render implements expr.Renderable.
func (e PosixSubstring) Render() (expr.Rendering, error) {
	spec := expr.Rendering{Tree: fmt.Sprintf("PosixSubstring syntax %d", e.Syntax), Inputs: e.Args[:], Form: expr.FormCall, Name: `"substring"`, Quote: expr.QuoteVerbatim, Args: hiddenArgs(e.Args[0], e.Args[1])}
	if e.Syntax == parser.SQLFunctionSyntax {
		// PostgreSQL displays the call; replay keeps the SQL syntax.
		text, arg := expr.TextPiece, expr.ArgPiece
		spec.Replay.Pieces = []expr.Piece{text("SUBSTRING("), arg(0), text(" FROM "), arg(1), text(")")}
	}
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e Posix) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Posix)
	return ok && e.Provider == o.Provider && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
}

// SameGroup implements expr.GroupIdentity.
func (e PosixSubstring) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(PosixSubstring)
	return ok && e.Provider == o.Provider && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}

// PreflightFold implements expr.Preflighting.
func (e Posix) PreflightFold() expr.Preflight { return expr.PreflightRegexNullOrEvaluate }

// PreflightFold implements expr.Preflighting.
func (e PosixSubstring) PreflightFold() expr.Preflight { return expr.PreflightRegexNullOrEvaluate }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e PosixSubstring) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e PosixSubstring) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("PosixSubstring", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// Substitute implements expr.Substitutable.
func (e Posix) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Posix", children, 2); err != nil {
		return nil, err
	}
	e.Info, e.Left, e.Right = info, children[0], children[1]
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e Posix) ClosureTransparent() {}

// ClosureTransparent implements expr.ClosureTransparent.
func (e PosixSubstring) ClosureTransparent() {}

func (e Posix) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: [2]expr.Expr{e.Left, e.Right}, Count: 2}, true, nil
}

func (e PosixSubstring) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: len(e.Args), Indexed: true, PosixSubstring: true}, true, nil
}

func (e PosixSubstring) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(e.Args[:], len(e.Args), written)
}
