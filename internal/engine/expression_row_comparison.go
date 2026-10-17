package engine

import (
	"fmt"
	"strconv"
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
	// Frozen erased constructor casts retain their analyzed member usages.
	memberCasts [2]bool
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

// IN recognizes transformed constructors too, but raw fields must retain unknown
// occurrences until field selection. Scalar use materializes these same children.
type inComparisonOperand struct {
	scalar      comparisonOperand
	fields      []comparisonOperand
	expansions  map[int]compositeExpansionBinding
	constructor bool
}

func (b *expressionBinder) inComparisonOperand(raw parser.Expr) (inComparisonOperand, error) {
	if row, ok := raw.(parser.RowExpr); ok {
		fields, expansions, err := b.rowComparisonOperands(row)
		return inComparisonOperand{scalar: comparisonOperand{raw: raw}, fields: fields, expansions: expansions, constructor: true}, err
	}
	operand, err := b.comparisonOperand(raw)
	result := inComparisonOperand{scalar: operand}
	if row, ok := operand.node.(rowExpression); ok && row.raw != nil && !row.usage.Type.IsDomain() {
		result.constructor, result.expansions = true, row.expansions
		result.fields = make([]comparisonOperand, len(row.fields))
		for i, field := range row.fields {
			result.fields[i] = comparisonOperand{node: field}
		}
	}
	return result, err
}

// Raw constructors participate in common-type selection as RECORD, without
// finalizing their unknown fields. This analysis-only node is never executed:
// RECORD cannot enter the scalar-array branch, and residual pairs use fields.
func (b *expressionBinder) inCommonOperand(operand inComparisonOperand) comparisonOperand {
	if operand.constructor && operand.scalar.node == nil {
		return comparisonOperand{node: rowExpression{expressionInfo: b.info(b.typ(2249).Usage(), operand.scalar.raw.SourcePosition(), ExprProperties{})}, raw: operand.scalar.raw}
	}
	return operand.scalar
}

func (b *expressionBinder) inScalarOperand(operand *inComparisonOperand) (comparisonOperand, error) {
	if operand.scalar.node != nil || !operand.constructor {
		return operand.scalar, nil
	}
	fields := make([]BoundExpr, len(operand.fields))
	members := make([]sqltype.CompositeMember, len(fields))
	properties := ExprProperties{}
	for i, field := range operand.fields {
		target := field.descriptor()
		if target == nil {
			target = b.typ(sqltype.Text)
		}
		node, err := b.finalizeOperand(field, target)
		if err != nil {
			return comparisonOperand{}, err
		}
		fields[i] = node
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: "f" + strconv.Itoa(i+1), Usage: node.ResultUsage()}
		properties = mergeExpressionProperties(properties, node.Properties())
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return comparisonOperand{}, err
	}
	raw := operand.scalar.raw.(parser.RowExpr)
	operand.scalar.node = rowExpression{expressionInfo: b.info(shape.Usage(), raw.Pos, properties), fields: fields, raw: &raw, expansions: operand.expansions}
	return operand.scalar, nil
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
	return b.bindRowPredicateOperands(raw, operator, lhs, rhs, le, re, mode)
}

func (b *expressionBinder) bindRowPredicateOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, lhs, rhs []comparisonOperand, le, re map[int]compositeExpansionBinding, mode string) (BoundExpr, error) {
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
	if !e.distinct() {
		if err := b.checkRowComparisonInterpretation(raw, e.comparisons); err != nil {
			return nil, err
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
func rowSubqueryComparison(raw parser.OperatorExpr) (parser.SubqueryExpr, bool) {
	if len(raw.Args) != 2 {
		return parser.SubqueryExpr{}, false
	}
	left, ok := raw.Args[0].(parser.RowExpr)
	if !ok {
		return parser.SubqueryExpr{}, false
	}
	right, ok := raw.Args[1].(parser.SubqueryExpr)
	if !ok || right.Kind != parser.SubqueryScalar {
		return parser.SubqueryExpr{}, false
	}
	right.Kind, right.Left, right.Operator, right.Pos = parser.SubqueryRow, left, raw.Name, raw.Pos
	return right, true
}

// A saved subquery tuple supplies left values. Local rows pass nil so each
// reached pair evaluates left then right before deciding whether to continue.
func (e rowComparisonExpression) compare(c *EvalContext, left []sqltype.Value) (sqltype.Value, error) {
	unknown := false
	for i, comparison := range e.comparisons {
		var valueLeft sqltype.Value
		if left == nil {
			var err error
			valueLeft, err = comparison.left.Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
		} else {
			valueLeft = left[i]
		}
		right, err := comparison.right.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.distinct() {
			distinct := valueLeft.IsNull() != right.IsNull()
			if !valueLeft.IsNull() && !right.IsNull() {
				value, err := comparison.predicate.Evaluate(c.Context, valueLeft, right, c.Input)
				if err != nil {
					return sqltype.Value{}, err
				}
				equal, err := value.Boolean()
				if err != nil {
					return sqltype.Value{}, err
				}
				distinct = !equal
			}
			if distinct {
				return sqltype.Boolean(e.usage.Type, e.operator == "IS DISTINCT FROM"), nil
			}
			continue
		}
		if len(e.comparisons) == 1 {
			return comparison.predicate.Evaluate(c.Context, valueLeft, right, c.Input)
		}
		if len(e.ordering) != 0 {
			if valueLeft.IsNull() || right.IsNull() {
				return sqltype.Null(e.usage.Type), nil
			}
			sign, err := e.ordering[i].Compare(c.Context, valueLeft, right, c.Input)
			if err != nil {
				return sqltype.Value{}, err
			}
			if sign == 0 {
				continue
			}
			truth := sign < 0
			if e.operator == ">" || e.operator == ">=" {
				truth = sign > 0
			}
			return sqltype.Boolean(e.usage.Type, truth), nil
		}
		value, err := comparison.predicate.Evaluate(c.Context, valueLeft, right, c.Input)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			unknown = true
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.operator == "<>" {
			if truth {
				return sqltype.Boolean(e.usage.Type, true), nil
			}
		} else if !truth {
			return sqltype.Boolean(e.usage.Type, false), nil
		}
	}
	if unknown {
		return sqltype.Null(e.usage.Type), nil
	}
	truth := e.operator == "=" || e.operator == "<=" || e.operator == ">=" || e.operator == "IS NOT DISTINCT FROM"
	return sqltype.Boolean(e.usage.Type, truth), nil
}
