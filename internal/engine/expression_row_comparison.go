package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Row predicates retain scalar pair providers, never a fabricated record datum.
// The lexical evaluator is shared with existing row/subquery predicates.
type rowComparisonExpression struct {
	expressionInfo
	comparisons []comparisonExpression
	ordering    []sqltype.Comparison
	operator    string
}

func (e rowComparisonExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return e.compare(c, nil)
}

func (b *expressionBinder) bindRowComparison(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right parser.RowExpr) (BoundExpr, error) {
	if len(left.Fields) != len(right.Fields) {
		return nil, expressionError("42601", "unequal number of entries in row expressions", raw.Pos)
	}
	if len(left.Fields) == 0 {
		return nil, expressionError("0A000", "cannot compare rows of zero length", raw.Pos)
	}
	e := rowComparisonExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, ExprProperties{}), operator: raw.Name.Parts[len(raw.Name.Parts)-1].Text, comparisons: make([]comparisonExpression, len(left.Fields))}
	if operator > sqltype.CompareNotEqual {
		e.ordering = make([]sqltype.Comparison, len(left.Fields))
	}
	for i := range left.Fields {
		l, err := b.comparisonOperand(left.Fields[i])
		if err != nil {
			return nil, err
		}
		r, err := b.comparisonOperand(right.Fields[i])
		if err != nil {
			return nil, err
		}
		node, err := b.bindComparisonOperands(raw, operator, l, r)
		if err != nil {
			return nil, err
		}
		pair, ok := node.(comparisonExpression)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid row comparison pair")
		}
		e.comparisons[i] = pair
		e.properties = mergeExpressionProperties(e.properties, pair.Properties())
		if len(e.ordering) != 0 {
			ref, conflict := pair.predicate.InputCollation()
			e.ordering[i], err = sqltype.BindComparison(pair.left.ResultUsage().Type, pair.right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	return e, nil
}

func (e subqueryExpression) compare(c *EvalContext, left []sqltype.Value) (sqltype.Value, error) {
	pairs := rowComparisonExpression{expressionInfo: e.expressionInfo, comparisons: e.comparisons, ordering: e.ordering, operator: e.operator}
	return pairs.compare(c, left)
}

func renderRowComparison(e rowComparisonExpression, operand func(BoundExpr) (string, error), operator string) (string, error) {
	if len(e.comparisons) == 0 {
		return "", fmt.Errorf("stuntgres: invalid stored row comparison")
	}
	left, right := make([]string, len(e.comparisons)), make([]string, len(e.comparisons))
	for i, pair := range e.comparisons {
		var err error
		left[i], err = operand(pair.left)
		if err != nil {
			return "", err
		}
		right[i], err = operand(pair.right)
		if err != nil {
			return "", err
		}
	}
	return "((" + strings.Join(left, ", ") + ") " + operator + " (" + strings.Join(right, ", ") + "))", nil
}
