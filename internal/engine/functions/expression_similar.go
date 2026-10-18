package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Similar struct {
	expr.Info
	Left, Right expr.Expr
	Provider    sqltype.SimilarPlan
	Operator    string
}
type SimilarEscape struct {
	expr.Info
	Pattern, Escape expr.Expr
	Provider        sqltype.SimilarEscapePlan
	Omitted         bool
}
type SimilarSubstring struct {
	expr.Info
	Args     [3]expr.Expr
	Provider sqltype.SimilarSubstringPlan
	Syntax   parser.FunctionSyntax
}
type LikeEscape struct {
	expr.Info
	Pattern, Escape expr.Expr
	Provider        sqltype.LikeEscapePlan
}

func (e Similar) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e SimilarEscape) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e SimilarSubstring) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e LikeEscape) Eval(c expr.Context) (sqltype.Value, error) {
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
func CoerceSimilarSubstringArguments(b ArgumentCoercer, operands []Argument, text sqltype.Usage) (args [3]expr.Expr, err error) {
	for i, operand := range operands {
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: LikeArgument, Target: text})
		if err != nil {
			return args, err
		}
	}
	return args, nil
}
func LikeEscapeTarget(operands [2]Argument) sqltype.ID {
	for _, operand := range operands {
		if d := operand.Usage.Type; d != nil && d.BaseType().ID() == sqltype.Bytea {
			return sqltype.Bytea
		}
	}
	return sqltype.Text
}
func LikeEscapeAllowed(visible, variadic bool, operands [2]Argument, target sqltype.ID) bool {
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
func CoerceLikeEscapeArguments(b ArgumentCoercer, operands [2]Argument, target sqltype.Usage) (pattern, escape expr.Expr, err error) {
	pattern, err = b.CoerceArgument(operands[0], CoercionRequest{Intent: LikeArgument, Target: target})
	if err != nil {
		return pattern, escape, err
	}
	escape, err = b.CoerceArgument(operands[1], CoercionRequest{Intent: LikeArgument, Target: target})
	return pattern, escape, err
}
func NewSimilar(info expr.Info, left, right expr.Expr, operator string, collation sqltype.CollationRef, conflict bool) (Similar, error) {
	provider, err := sqltype.BindSimilar(operator, left.ResultUsage(), right.ResultUsage(), info.Usage, collation, conflict)
	return Similar{info, left, right, provider, operator}, err
}
func (e Similar) Reconstruct(info expr.Info, left, right expr.Expr, collation sqltype.CollationRef, conflict bool) (Similar, error) {
	return NewSimilar(info, left, right, e.Operator, collation, conflict)
}
func NewSimilarEscape(info expr.Info, pattern, escape expr.Expr, omitted bool) (SimilarEscape, error) {
	provider, err := sqltype.BindSimilarEscape(pattern.ResultUsage(), escape.ResultUsage(), info.Usage)
	return SimilarEscape{info, pattern, escape, provider, omitted}, err
}
func (e SimilarEscape) Reconstruct(info expr.Info, pattern, escape expr.Expr) (SimilarEscape, error) {
	return NewSimilarEscape(info, pattern, escape, e.Omitted)
}

// SimilarEscape implements expr.Composite.
var _ expr.Composite = SimilarEscape{}

func (e SimilarEscape) Children() []expr.Expr { return []expr.Expr{e.Pattern, e.Escape} }
func (e SimilarEscape) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("SimilarEscape", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children[0], children[1]))
}
func NewSimilarSubstring(info expr.Info, args [3]expr.Expr, syntax parser.FunctionSyntax, wrapper bool) (SimilarSubstring, error) {
	usages := [3]sqltype.Usage{args[0].ResultUsage(), args[1].ResultUsage(), args[2].ResultUsage()}
	provider, err := sqltype.BindSimilarSubstring(usages, info.Usage, wrapper)
	return SimilarSubstring{info, args, provider, syntax}, err
}
func (e SimilarSubstring) Reconstruct(info expr.Info, args [3]expr.Expr) (SimilarSubstring, error) {
	return NewSimilarSubstring(info, args, e.Syntax, e.Provider.WrapperContext())
}

// SimilarSubstring implements expr.Composite.
var _ expr.Composite = SimilarSubstring{}

func (e SimilarSubstring) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e SimilarSubstring) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("SimilarSubstring", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}
func NewLikeEscape(info expr.Info, pattern, escape expr.Expr) (LikeEscape, error) {
	provider, err := sqltype.BindLikeEscape(pattern.ResultUsage(), escape.ResultUsage(), info.Usage)
	return LikeEscape{info, pattern, escape, provider}, err
}
func (e LikeEscape) Reconstruct(info expr.Info, pattern, escape expr.Expr) (LikeEscape, error) {
	return NewLikeEscape(info, pattern, escape)
}

// LikeEscape implements expr.Composite.
var _ expr.Composite = LikeEscape{}

func (e LikeEscape) Children() []expr.Expr { return []expr.Expr{e.Pattern, e.Escape} }
func (e LikeEscape) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("LikeEscape", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children[0], children[1]))
}

// Similar implements expr.Composite. Rebuild keeps the bound input collation.
var _ expr.Composite = Similar{}

func (e Similar) Children() []expr.Expr { return []expr.Expr{e.Left, e.Right} }
func (e Similar) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Similar", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children[0], children[1], e.Provider.Collation(), e.Provider.Conflict()))
}

func (e LikeEscape) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e SimilarEscape) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e SimilarSubstring) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Similar) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
