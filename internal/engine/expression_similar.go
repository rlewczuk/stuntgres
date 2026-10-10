package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type similarExpression struct {
	expressionInfo
	left, right BoundExpr
	provider    sqltype.SimilarPlan
	operator    string
}
type similarEscapeExpression struct {
	expressionInfo
	pattern, escape BoundExpr
	provider        sqltype.SimilarEscapePlan
	omitted         bool
}
type similarSubstringExpression struct {
	expressionInfo
	args     [3]BoundExpr
	provider sqltype.SimilarSubstringPlan
	syntax   parser.FunctionSyntax
}

func similarExecution(c *EvalContext) *sqltype.SimilarExecution {
	if c.statementScope != nil {
		return &c.statementScope.similar
	}
	return &c.similar
}
func (e similarExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	// Constant normalizers are preflighted separately; a row-dependent helper
	// is not demanded when the strict source argument is NULL.
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() {
		similarExecution(c).Reset()
		return sqltype.Null(e.usage.Type), nil
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(c.Context, a, b, similarExecution(c))
}
func (e similarEscapeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	a, err := e.pattern.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() {
		similarExecution(c).Reset()
		return sqltype.Null(e.usage.Type), nil
	}
	b, err := e.escape.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(c.Context, a, b, similarExecution(c))
}
func (e similarSubstringExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.provider.Evaluate(c.Context, values, similarExecution(c))
}

func similarTextType(d *sqltype.Descriptor) bool {
	if d == nil {
		return true
	}
	switch d.BaseType().ID() {
	case sqltype.Text, sqltype.Name, sqltype.BPChar, sqltype.VarChar:
		return true
	}
	return false
}
func (b *expressionBinder) bindSimilar(raw parser.OperatorExpr) (BoundExpr, error) {
	if !raw.SQLSimilar || len(raw.Args) != 2 || len(raw.Name.Parts) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid SQL SIMILAR expression")
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	if !similarTextType(left.descriptor()) || !similarTextType(right.descriptor()) {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left.descriptor(), right.descriptor(), raw)
	}
	target := b.typ(sqltype.Text)
	if d := left.descriptor(); d != nil && (d.BaseType().ID() == sqltype.Name || d.BaseType().ID() == sqltype.BPChar) {
		target = d.BaseType()
	}
	l, err := b.likeOperand(left, target)
	if err != nil {
		return nil, err
	}
	r, err := b.likeOperand(right, b.typ(sqltype.Text))
	if err != nil {
		return nil, err
	}
	collation, err := collatedInfo(b.info(l.ResultUsage(), raw.Pos, ExprProperties{}), l, r)
	if err != nil {
		return nil, err
	}
	op := raw.Name.Parts[0].Text
	result := b.typ(sqltype.Bool).Usage()
	provider, err := sqltype.BindSimilar(op, l.ResultUsage(), r.ResultUsage(), result, collation.usage.Collation, collation.collationConflict)
	if err != nil {
		return nil, err
	}
	return similarExpression{b.info(result, raw.Pos, likeProperties(l, r)), l, r, provider, op}, nil
}
func (b *expressionBinder) bindSimilarEscape(raw parser.FunctionExpr) (BoundExpr, error) {
	if len(raw.Args) < 1 || len(raw.Args) > 2 || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "pg_catalog" || raw.Name.Parts[1].Text != "similar_to_escape" {
		return nil, b.functionSignatureError(raw)
	}
	var operands [2]comparisonOperand
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		operands[i] = o
		names[i] = "unknown"
		if d := o.descriptor(); d != nil {
			names[i] = d.DiagnosticName()
		}
	}
	for i := range raw.Args {
		if !similarTextType(operands[i].descriptor()) {
			return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
		}
	}
	pattern, err := b.likeOperand(operands[0], b.typ(sqltype.Text))
	if err != nil {
		return nil, err
	}
	var escape BoundExpr
	omitted := len(raw.Args) == 1
	if omitted {
		u := b.typ(sqltype.Text).Usage()
		escape = literalExpression{expressionInfo: b.info(u, raw.Pos, ExprProperties{Volatility: Immutable}), value: sqltype.String(u.Type, `\`)}
	} else {
		escape, err = b.likeOperand(operands[1], b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
	}
	info, err := collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, likeProperties(pattern, escape)), pattern, escape)
	if err != nil {
		return nil, err
	}
	provider, err := sqltype.BindSimilarEscape(pattern.ResultUsage(), escape.ResultUsage(), info.usage)
	if err != nil {
		return nil, err
	}
	return similarEscapeExpression{info, pattern, escape, provider, omitted}, nil
}
func (b *expressionBinder) bindSimilarSubstring(raw parser.FunctionExpr, operands []comparisonOperand) (BoundExpr, error) {
	var args [3]BoundExpr
	var usages [3]sqltype.Usage
	props := ExprProperties{Volatility: Immutable}
	wrapper := b.defaultConsumer
	for i, o := range operands {
		arg, err := b.likeOperand(o, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
		args[i], usages[i] = arg, arg.ResultUsage()
		p := arg.Properties()
		props.Volatility = max(props.Volatility, p.Volatility)
		props.RowDependent = props.RowDependent || p.RowDependent
		props.ParameterDependent = props.ParameterDependent || p.ParameterDependent
		props.StageDependent = props.StageDependent || p.StageDependent
		if !similarLiteralRaw(raw.Args[i]) {
			wrapper = true
		}
	}
	info, err := collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, props), args[:]...)
	if err != nil {
		return nil, err
	}
	provider, err := sqltype.BindSimilarSubstring(usages, info.usage, wrapper)
	if err != nil {
		return nil, err
	}
	return similarSubstringExpression{info, args, provider, raw.Syntax}, nil
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
