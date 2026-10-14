package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Posix[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Left, Right         E
	Provider            sqltype.PosixPlan
	Operator, Namespace string
}
type PosixSubstring[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Args     [2]E
	Provider sqltype.PosixSubstringPlan
	Syntax   parser.FunctionSyntax
}

func (e Posix[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e PosixSubstring[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func CoercePatternArguments[E any, B ArgumentCoercer[E]](b B, left, right Argument[E], text sqltype.Usage) (l, r E, err error) {
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
func CoercePosixSubstringArguments[E any, B ArgumentCoercer[E]](b B, operands []Argument[E], text sqltype.Usage) (args [2]E, err error) {
	for i, operand := range operands {
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: LikeArgument, Target: text})
		if err != nil {
			return args, err
		}
	}
	return args, nil
}
func NewPosix[E Expression[C], C RegexpContext, P any](info Info[P], left, right E, operator, namespace string, collation sqltype.CollationRef, conflict bool) (Posix[E, C, P], error) {
	provider, err := sqltype.BindPosix(operator, left.ResultUsage(), right.ResultUsage(), info.Usage, collation, conflict)
	return Posix[E, C, P]{info, left, right, provider, operator, namespace}, err
}
func (e Posix[E, C, P]) Reconstruct(info Info[P], left, right E, collation sqltype.CollationRef, conflict bool) (Posix[E, C, P], error) {
	return NewPosix[E, C](info, left, right, e.Operator, e.Namespace, collation, conflict)
}
func NewPosixSubstring[E Expression[C], C RegexpContext, P any](info Info[P], args [2]E, syntax parser.FunctionSyntax) (PosixSubstring[E, C, P], error) {
	usages := [2]sqltype.Usage{args[0].ResultUsage(), args[1].ResultUsage()}
	provider, err := sqltype.BindPosixSubstring(usages, info.Usage, info.Usage.Collation, info.CollationConflict)
	return PosixSubstring[E, C, P]{info, args, provider, syntax}, err
}
func (e PosixSubstring[E, C, P]) Reconstruct(info Info[P], args [2]E) (PosixSubstring[E, C, P], error) {
	return NewPosixSubstring[E, C](info, args, e.Syntax)
}
