package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRowComparison(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right parser.RowExpr) (expr.Expr, error) {
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
		return nil, nil, expr.ErrorAt("54011", "ROW expressions can have at most 1664 entries", raw.Pos)
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
	if row, ok := operand.node.(rowExpression); ok && row.Raw != nil && !row.Usage.Type.IsDomain() {
		result.constructor, result.expansions = true, row.Expansions
		result.fields = make([]comparisonOperand, row.Usage.Type.BaseType().CompositeLen())
		for i, field := range row.Fields {
			slot := i
			if row.FieldProjection != nil {
				slot = row.FieldProjection[i].Slot
				if slot < 0 {
					continue
				}
			}
			result.fields[slot] = comparisonOperand{node: field}
		}
	}
	return result, err
}

// Raw constructors participate in common-type selection as RECORD, without
// finalizing their unknown fields. This analysis-only node is never executed:
// RECORD cannot enter the scalar-array branch, and residual pairs use fields.
func (b *expressionBinder) inCommonOperand(operand inComparisonOperand) comparisonOperand {
	if operand.constructor && operand.scalar.node == nil {
		return comparisonOperand{node: rowExpression{Info: b.info(b.typ(2249).Usage(), operand.scalar.raw.SourcePosition(), expr.Properties{})}, raw: operand.scalar.raw}
	}
	return operand.scalar
}

func (b *expressionBinder) inScalarOperand(operand *inComparisonOperand) (comparisonOperand, error) {
	if operand.scalar.node != nil || !operand.constructor {
		return operand.scalar, nil
	}
	fields := make([]expr.Expr, len(operand.fields))
	members := make([]sqltype.CompositeMember, len(fields))
	properties := expr.Properties{}
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
		properties = expr.MergeProperties(properties, node.Properties())
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return comparisonOperand{}, err
	}
	raw := operand.scalar.raw.(parser.RowExpr)
	operand.scalar.node = rowExpression{Info: b.info(shape.Usage(), raw.Pos, properties), Fields: fields, Raw: &raw, Expansions: operand.expansions}
	return operand.scalar, nil
}

func (b *expressionBinder) bindRowPredicate(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right parser.RowExpr, mode string) (expr.Expr, error) {
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

func (b *expressionBinder) bindRowPredicateOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, lhs, rhs []comparisonOperand, le, re map[int]compositeExpansionBinding, mode string) (expr.Expr, error) {
	if len(lhs) != len(rhs) {
		return nil, expr.ErrorAt("42601", "unequal number of entries in row expressions", raw.Pos)
	}
	e := rowComparisonExpression{Info: b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, expr.Properties{}), Operator: mode, Expansions: [2]map[int]compositeExpansionBinding{le, re}}
	if len(lhs) == 0 && !e.Distinct() {
		return nil, expr.ErrorAt("0A000", "cannot compare rows of zero length", raw.Pos)
	}
	e.Comparisons = make([]comparisonExpression, len(lhs))
	if operator > sqltype.CompareNotEqual {
		e.Ordering = make([]sqltype.Comparison, len(lhs))
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
		e.Comparisons[i] = pair
		e.Property = expr.MergeProperties(e.Property, pair.Properties())
		if len(e.Ordering) != 0 {
			ref, conflict := pair.Predicate.InputCollation()
			e.Ordering[i], err = sqltype.BindComparison(pair.Left.ResultUsage().Type, pair.Right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	if !e.Distinct() {
		if err := b.checkRowComparisonInterpretation(raw, e.Comparisons); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func renderRowComparison(e rowComparisonExpression, operand func(expr.Expr) (string, error), operator string) (string, error) {
	if len(e.Comparisons) == 0 && !e.Distinct() {
		return "", fmt.Errorf("stuntgres: invalid stored row comparison")
	}
	left, right := make([]string, len(e.Comparisons)), make([]string, len(e.Comparisons))
	for i, pair := range e.Comparisons {
		var err error
		left[i], err = operand(pair.Left)
		if err != nil {
			return "", err
		}
		right[i], err = operand(pair.Right)
		if err != nil {
			return "", err
		}
	}
	constructor := ""
	if len(e.Comparisons) < 2 {
		constructor = "ROW"
	}
	return "(" + constructor + "(" + strings.Join(left, ", ") + ") " + operator + " " + constructor + "(" + strings.Join(right, ", ") + "))", nil
}

// Reuse the constructor's copy-on-change ownership mapping for analysis-only
// expansion associations; executable pairs are mapped by their existing owners.
func mapRowComparisonExpansions(expansions map[int]compositeExpansionBinding, child func(expr.Expr) (expr.Expr, bool, error)) (map[int]compositeExpansionBinding, bool, error) {
	row, changed, err := mapRowAnalysis(rowExpression{Expansions: expansions}, child)
	return row.Expansions, changed, err
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
