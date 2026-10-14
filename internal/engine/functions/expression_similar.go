package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Similar[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Left, Right E
	Provider    sqltype.SimilarPlan
	Operator    string
}
type SimilarEscape[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Pattern, Escape E
	Provider        sqltype.SimilarEscapePlan
	Omitted         bool
}
type SimilarSubstring[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Args     [3]E
	Provider sqltype.SimilarSubstringPlan
	Syntax   parser.FunctionSyntax
}
type LikeEscape[E Expression[C], C LiteralContext[E], P any] struct {
	Info[P]
	Pattern, Escape E
	Provider        sqltype.LikeEscapePlan
}

func (e Similar[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	// Constant normalizers are preflighted separately; a row-dependent helper
	// is not demanded when the strict source argument is NULL.
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
func (e SimilarEscape[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	a, err := e.Pattern.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() {
		c.FunctionRegexExecution().Reset()
		return sqltype.Null(e.Usage.Type), nil
	}
	b, err := e.Escape.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Evaluate(ctx, a, b, c.FunctionRegexExecution())
}
func (e SimilarSubstring[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Provider.Evaluate(ctx, values, c.FunctionRegexExecution())
}
func (e LikeEscape[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Pattern) || c.FunctionLiteralNull(e.Escape) {
		return sqltype.Null(e.Usage.Type), nil
	}
	pattern, err := e.Pattern.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	escape, err := e.Escape.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Evaluate(ctx, pattern, escape)
}
func SimilarShape(raw parser.OperatorExpr) bool {
	return raw.SQLSimilar && len(raw.Args) == 2 && len(raw.Name.Parts) == 1
}
func SimilarEscapeShape(raw parser.FunctionExpr) (valid, omitted bool) {
	return len(raw.Args) >= 1 && len(raw.Args) <= 2 && len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == "pg_catalog" && raw.Name.Parts[1].Text == "similar_to_escape", len(raw.Args) == 1
}
func SimilarSubstringWrapper(raw []parser.Expr, defaultConsumer bool) bool {
	if defaultConsumer {
		return true
	}
	for _, arg := range raw {
		if !similarLiteralRaw(arg) {
			return true
		}
	}
	return false
}
func similarLiteralRaw(raw parser.Expr) bool {
	switch r := raw.(type) {
	case parser.LiteralExpr:
		return true
	case parser.CastExpr:
		return similarLiteralRaw(r.Child)
	case parser.CollateExpr:
		return similarLiteralRaw(r.Child)
	}
	return false
}
func CoerceSimilarSubstringArguments[E any, B ArgumentCoercer[E]](b B, operands []Argument[E], text sqltype.Usage) (args [3]E, err error) {
	for i, operand := range operands {
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: LikeArgument, Target: text})
		if err != nil {
			return args, err
		}
	}
	return args, nil
}
func LikeEscapeTarget[E any](operands [2]Argument[E]) sqltype.ID {
	for _, operand := range operands {
		if d := operand.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Bytea {
			return sqltype.Bytea
		}
	}
	return sqltype.Text
}
func LikeEscapeAllowed[E any](visible, variadic bool, operands [2]Argument[E], target sqltype.ID) bool {
	if !visible || variadic {
		return false
	}
	for _, operand := range operands {
		if d := operand.Usage.Type; d != nil {
			if target == sqltype.Bytea {
				if d.BaseType().ID() != sqltype.Bytea {
					return false
				}
			} else if !PatternTextType(d) {
				return false
			}
		}
	}
	return true
}
func CoerceLikeEscapeArguments[E any, B ArgumentCoercer[E]](b B, operands [2]Argument[E], target sqltype.Usage) (pattern, escape E, err error) {
	pattern, err = b.CoerceArgument(operands[0], CoercionRequest{Intent: LikeArgument, Target: target})
	if err != nil {
		return pattern, escape, err
	}
	escape, err = b.CoerceArgument(operands[1], CoercionRequest{Intent: LikeArgument, Target: target})
	return pattern, escape, err
}
func NewSimilar[E Expression[C], C RegexpContext, P any](info Info[P], left, right E, operator string, collation sqltype.CollationRef, conflict bool) (Similar[E, C, P], error) {
	provider, err := sqltype.BindSimilar(operator, left.ResultUsage(), right.ResultUsage(), info.Usage, collation, conflict)
	return Similar[E, C, P]{info, left, right, provider, operator}, err
}
func (e Similar[E, C, P]) Reconstruct(info Info[P], left, right E, collation sqltype.CollationRef, conflict bool) (Similar[E, C, P], error) {
	return NewSimilar[E, C](info, left, right, e.Operator, collation, conflict)
}
func NewSimilarEscape[E Expression[C], C RegexpContext, P any](info Info[P], pattern, escape E, omitted bool) (SimilarEscape[E, C, P], error) {
	provider, err := sqltype.BindSimilarEscape(pattern.ResultUsage(), escape.ResultUsage(), info.Usage)
	return SimilarEscape[E, C, P]{info, pattern, escape, provider, omitted}, err
}
func (e SimilarEscape[E, C, P]) Reconstruct(info Info[P], pattern, escape E) (SimilarEscape[E, C, P], error) {
	return NewSimilarEscape[E, C](info, pattern, escape, e.Omitted)
}
func NewSimilarSubstring[E Expression[C], C RegexpContext, P any](info Info[P], args [3]E, syntax parser.FunctionSyntax, wrapper bool) (SimilarSubstring[E, C, P], error) {
	usages := [3]sqltype.Usage{args[0].ResultUsage(), args[1].ResultUsage(), args[2].ResultUsage()}
	provider, err := sqltype.BindSimilarSubstring(usages, info.Usage, wrapper)
	return SimilarSubstring[E, C, P]{info, args, provider, syntax}, err
}
func (e SimilarSubstring[E, C, P]) Reconstruct(info Info[P], args [3]E) (SimilarSubstring[E, C, P], error) {
	return NewSimilarSubstring[E, C](info, args, e.Syntax, e.Provider.WrapperContext())
}
func NewLikeEscape[E Expression[C], C LiteralContext[E], P any](info Info[P], pattern, escape E) (LikeEscape[E, C, P], error) {
	provider, err := sqltype.BindLikeEscape(pattern.ResultUsage(), escape.ResultUsage(), info.Usage)
	return LikeEscape[E, C, P]{info, pattern, escape, provider}, err
}
func (e LikeEscape[E, C, P]) Reconstruct(info Info[P], pattern, escape E) (LikeEscape[E, C, P], error) {
	return NewLikeEscape[E, C](info, pattern, escape)
}
