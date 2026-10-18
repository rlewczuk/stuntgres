package functions

import (
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
