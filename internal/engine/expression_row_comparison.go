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
	// Analysis-only associations retain original expanded source occurrences.
	expansions [2]map[int]compositeExpansionBinding
}

func (e rowComparisonExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return e.compare(c, nil)
}

func (b *expressionBinder) bindRowComparison(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right parser.RowExpr) (BoundExpr, error) {
	return b.bindRowPredicate(raw, operator, left, right, raw.Name.Parts[len(raw.Name.Parts)-1].Text)
}

// Analyze constructors without allocating RECORD values or finalizing unknowns.
func (b *expressionBinder) rowComparisonOperands(raw parser.RowExpr) ([]comparisonOperand, map[int]compositeExpansionBinding, error) {
	list, err := b.expandRawList(raw.Fields)
	if err != nil {
		return nil, nil, err
	}
	fields := make([]comparisonOperand, len(list.raw))
	for i, field := range list.raw {
		if list.nodes != nil && list.nodes[i] != nil {
			fields[i] = comparisonOperand{node: list.nodes[i], raw: field}
		} else {
			fields[i], err = b.comparisonOperand(field)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if len(fields) > 1664 {
		return nil, nil, expressionError("54011", "ROW expressions can have at most 1664 entries", raw.Pos)
	}
	return fields, list.expansions, nil
}

func (e rowComparisonExpression) distinct() bool {
	return e.operator == "IS DISTINCT FROM" || e.operator == "IS NOT DISTINCT FROM"
}

func (b *expressionBinder) bindRowPredicate(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right parser.RowExpr, mode string) (BoundExpr, error) {
	lhs, le, err := b.rowComparisonOperands(left)
	if err != nil {
		return nil, err
	}
	rhs, re, err := b.rowComparisonOperands(right)
	if err != nil {
		return nil, err
	}
	if len(lhs) != len(rhs) {
		return nil, expressionError("42601", "unequal number of entries in row expressions", raw.Pos)
	}
	e := rowComparisonExpression{expressionInfo: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, ExprProperties{}), operator: mode, expansions: [2]map[int]compositeExpansionBinding{le, re}}
	if len(lhs) == 0 && !e.distinct() {
		return nil, expressionError("0A000", "cannot compare rows of zero length", raw.Pos)
	}
	e.comparisons = make([]comparisonExpression, len(lhs))
	if operator > sqltype.CompareNotEqual {
		e.ordering = make([]sqltype.Comparison, len(lhs))
	}
	for i, l := range lhs {
		r := rhs[i]
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
	if len(e.comparisons) == 0 && !e.distinct() {
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
	constructor := ""
	if len(e.comparisons) < 2 {
		constructor = "ROW"
	}
	return "(" + constructor + "(" + strings.Join(left, ", ") + ") " + operator + " " + constructor + "(" + strings.Join(right, ", ") + "))", nil
}

// Reuse the constructor's copy-on-change ownership mapping for analysis-only
// expansion associations; executable pairs are mapped by their existing owners.
func mapRowComparisonExpansions(expansions map[int]compositeExpansionBinding, child func(BoundExpr) (BoundExpr, bool, error)) (map[int]compositeExpansionBinding, bool, error) {
	row, changed, err := mapRowAnalysis(rowExpression{expansions: expansions}, child)
	return row.expansions, changed, err
}
