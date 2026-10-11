package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type posixExpression struct {
	expressionInfo
	left, right         BoundExpr
	provider            sqltype.PosixPlan
	operator, namespace string
}

type posixSubstringExpression struct {
	expressionInfo
	args     [2]BoundExpr
	provider sqltype.PosixSubstringPlan
	syntax   parser.FunctionSyntax
}

func posixName(name string) bool {
	return name == "~" || name == "~*" || name == "!~" || name == "!~*"
}

func (e posixExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() {
		pgRegexExecution(c).Reset()
		return sqltype.Null(e.usage.Type), nil
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(c.Context, a, b, pgRegexExecution(c))
}

func (e posixSubstringExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			pgRegexExecution(c).Reset()
			return sqltype.Null(e.usage.Type), nil
		}
		values[i] = value
	}
	return e.provider.Evaluate(c.Context, values, pgRegexExecution(c))
}

func (b *expressionBinder) bindPosix(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid POSIX expression")
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible || !similarTextType(left.descriptor()) || !similarTextType(right.descriptor()) {
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
	collation, err := b.collatedInfo(b.info(l.ResultUsage(), raw.Pos, ExprProperties{}), l, r)
	if err != nil {
		return nil, err
	}
	op := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	result := b.typ(sqltype.Bool).Usage()
	provider, err := sqltype.BindPosix(op, l.ResultUsage(), r.ResultUsage(), result, collation.usage.Collation, collation.collationConflict)
	if err != nil {
		return nil, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return posixExpression{b.info(result, raw.Pos, likeProperties(l, r)), l, r, provider, op, namespace}, nil
}

func (b *expressionBinder) bindPosixSubstring(raw parser.FunctionExpr, operands []comparisonOperand) (BoundExpr, error) {
	var args [2]BoundExpr
	var usages [2]sqltype.Usage
	for i, operand := range operands {
		arg, err := b.likeOperand(operand, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
		args[i], usages[i] = arg, arg.ResultUsage()
	}
	info, err := b.collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, likeProperties(args[0], args[1])), args[:]...)
	if err != nil {
		return nil, err
	}
	provider, err := sqltype.BindPosixSubstring(usages, info.usage, info.usage.Collation, info.collationConflict)
	if err != nil {
		return nil, err
	}
	return posixSubstringExpression{info, args, provider, raw.Syntax}, nil
}
