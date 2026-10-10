package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This consumer deliberately does not change SELECT's exact-conjunction mask.
// It proves SQL TRUE => TRUE with the owned immutable comparison providers.
func conflictPredicateImpliedBy(ctx *EvalContext, a, b BoundExpr) (bool, error) {
	if b == nil {
		return true, nil
	}
	if a == nil {
		return false, nil
	}
	a, err := normalizeConflictPredicate(ctx, a)
	if err != nil {
		return false, err
	}
	b, err = normalizeConflictPredicate(ctx, b)
	if err != nil {
		return false, err
	}
	return conflictPredicateProof(ctx, a, b)
}
func normalizeConflictPredicate(ctx *EvalContext, node BoundExpr) (BoundExpr, error) {
	if node == nil {
		return nil, nil
	}
	p := node.Properties()
	if !p.RowDependent && !p.StageDependent && plannedVolatility(node) == Immutable {
		value, err := node.Eval(ctx)
		if err != nil {
			return nil, err
		}
		info := boundExpressionInfo(node)
		info.properties = ExprProperties{}
		return literalExpression{expressionInfo: info, value: value}, nil
	}
	switch n := node.(type) {
	case logicalExpression:
		left, err := normalizeConflictPredicate(ctx, n.left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.right)
		if err != nil {
			return nil, err
		}
		n.left, n.right = left, right
		if n.op == parser.LogicalNot {
			return negateConflictPredicate(n), nil
		}
		if truth, ok := conflictLiteralTruth(left); ok {
			if n.op == parser.LogicalAnd && truth == True || n.op == parser.LogicalOr && truth == False {
				return right, nil
			}
			if n.op == parser.LogicalAnd && truth == False || n.op == parser.LogicalOr && truth == True {
				return left, nil
			}
		}
		if truth, ok := conflictLiteralTruth(right); ok {
			if n.op == parser.LogicalAnd && truth == True || n.op == parser.LogicalOr && truth == False {
				return left, nil
			}
			if n.op == parser.LogicalAnd && truth == False || n.op == parser.LogicalOr && truth == True {
				return right, nil
			}
		}
		return n, nil
	case comparisonExpression:
		left, err := normalizeConflictPredicate(ctx, n.left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.right)
		if err != nil {
			return nil, err
		}
		n.left, n.right = left, right
		if n.operator == "=" && left.ResultUsage().Type.ID() == sqltype.Bool {
			if truth, ok := conflictLiteralTruth(right); ok {
				if truth == True {
					return left, nil
				}
				if truth == False {
					return negateConflictPredicate(logicalExpression{expressionInfo: n.expressionInfo, op: parser.LogicalNot, left: left}), nil
				}
			}
			if truth, ok := conflictLiteralTruth(left); ok {
				if truth == True {
					return right, nil
				}
				if truth == False {
					return negateConflictPredicate(logicalExpression{expressionInfo: n.expressionInfo, op: parser.LogicalNot, left: right}), nil
				}
			}
		}
		return n, nil
	case arithmeticExpression:
		left, err := normalizeConflictPredicate(ctx, n.left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.right)
		n.left, n.right = left, right
		return n, err
	case nullTestExpression:
		child, err := normalizeConflictPredicate(ctx, n.child)
		n.child = child
		return n, err
	case truthTestExpression:
		child, err := normalizeConflictPredicate(ctx, n.child)
		n.child = child
		return n, err
	case coercionExpression:
		child, err := normalizeConflictPredicate(ctx, n.child)
		n.child = child
		return n, err
	case collationExpression:
		child, err := normalizeConflictPredicate(ctx, n.child)
		n.child = child
		return n, err
	}
	return node, nil
}
func conflictLiteralTruth(node BoundExpr) (Truth, bool) {
	literal, ok := node.(literalExpression)
	if !ok || literal.value.Type() == nil || literal.value.Type().ID() != sqltype.Bool {
		return Unknown, false
	}
	truth, err := TruthOf(literal.value)
	return truth, err == nil
}
func negateConflictPredicate(n logicalExpression) BoundExpr {
	switch child := n.left.(type) {
	case logicalExpression:
		if child.op == parser.LogicalNot {
			return child.left
		}
		if child.op == parser.LogicalAnd || child.op == parser.LogicalOr {
			op := parser.LogicalAnd
			if child.op == parser.LogicalAnd {
				op = parser.LogicalOr
			}
			return logicalExpression{expressionInfo: n.expressionInfo, op: op, left: negateConflictPredicate(logicalExpression{expressionInfo: n.expressionInfo, op: parser.LogicalNot, left: child.left}), right: negateConflictPredicate(logicalExpression{expressionInfo: n.expressionInfo, op: parser.LogicalNot, left: child.right})}
		}
	case nullTestExpression:
		child.not = !child.not
		return child
	}
	return n
}
func conflictPredicateProof(ctx *EvalContext, a, b BoundExpr) (bool, error) {
	if plannedVolatility(a) != Immutable || plannedVolatility(b) != Immutable {
		return false, nil
	}
	if truth, ok := conflictLiteralTruth(b); ok && truth == True {
		return true, nil
	}
	if truth, ok := conflictLiteralTruth(a); ok && truth != True {
		return true, nil
	}
	if sameGroupExpression(a, b) {
		return true, nil
	}
	if right, ok := b.(logicalExpression); ok && right.op == parser.LogicalAnd {
		left, err := conflictPredicateProof(ctx, a, right.left)
		if err != nil || !left {
			return left, err
		}
		return conflictPredicateProof(ctx, a, right.right)
	}
	if left, ok := a.(logicalExpression); ok && left.op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, left.left, b)
		if err != nil || !first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.right, b)
	}
	if right, ok := b.(logicalExpression); ok && right.op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, a, right.left)
		if err != nil || first {
			return first, err
		}
		second, err := conflictPredicateProof(ctx, a, right.right)
		if err != nil || second {
			return second, err
		}
	}
	if left, ok := a.(logicalExpression); ok && left.op == parser.LogicalAnd {
		first, err := conflictPredicateProof(ctx, left.left, b)
		if err != nil || first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.right, b)
	}
	if required, ok := b.(nullTestExpression); ok && required.not {
		return conflictStrictFor(a, required.child), nil
	}
	return conflictComparisonProof(ctx, a, b)
}
func conflictStrictFor(node, subject BoundExpr) bool {
	if sameGroupExpression(node, subject) {
		return node.ResultUsage().Type.ID() == sqltype.Bool
	}
	if c, ok := node.(comparisonExpression); ok {
		return !c.predicate.SessionDependent() && (sameGroupExpression(c.left, subject) || sameGroupExpression(c.right, subject))
	}
	if n, ok := node.(logicalExpression); ok && n.op == parser.LogicalNot {
		return conflictStrictFor(n.left, subject)
	}
	return false
}

var conflictOperators = [6]string{"<", "<=", "=", ">=", ">", "<>"}
var conflictSameOperators = [6][6]bool{
	{true, true, false, false, false, true}, {false, true, false, false, false, false}, {false, true, true, true, false, false},
	{false, false, false, true, false, false}, {false, false, false, true, true, true}, {false, false, false, false, false, true},
}
var conflictConstantOperators = [6][6]string{
	{">=", ">=", "", "", "", ">="}, {">", ">=", "", "", "", ">"}, {">", ">=", "=", "<=", "<", "<>"},
	{"", "", "", "<=", "<", "<"}, {"", "", "", "<=", "<=", "<="}, {"", "", "", "", "", "="},
}

func conflictOperatorIndex(op string) int {
	for i, s := range conflictOperators {
		if op == s || s == "<>" && op == "!=" {
			return i
		}
	}
	return -1
}
func conflictCommuted(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

// A negated comparison view is proof-only: never replace an executable node's
// operator while retaining its original evaluator.
func conflictProofComparison(node BoundExpr) (comparisonExpression, bool) {
	if comparison, ok := node.(comparisonExpression); ok {
		return comparison, true
	}
	not, ok := node.(logicalExpression)
	if !ok || not.op != parser.LogicalNot {
		return comparisonExpression{}, false
	}
	comparison, ok := not.left.(comparisonExpression)
	if !ok {
		return comparisonExpression{}, false
	}
	switch comparison.operator {
	case "<":
		comparison.operator = ">="
	case "<=":
		comparison.operator = ">"
	case "=":
		comparison.operator = "<>"
	case ">=":
		comparison.operator = "<"
	case ">":
		comparison.operator = "<="
	case "<>", "!=":
		comparison.operator = "="
	default:
		return comparisonExpression{}, false
	}
	return comparison, true
}
func conflictComparisonProof(ctx *EvalContext, a, b BoundExpr) (bool, error) {
	x, ok := conflictProofComparison(a)
	if !ok {
		return false, nil
	}
	y, ok := conflictProofComparison(b)
	if !ok {
		return false, nil
	}
	if x.predicate.SessionDependent() || y.predicate.SessionDependent() || x.namespace != y.namespace {
		return false, nil
	}
	if _, ok := x.left.(literalExpression); ok {
		x.left, x.right = x.right, x.left
		x.operator = conflictCommuted(x.operator)
	}
	if _, ok := y.left.(literalExpression); ok {
		y.left, y.right = y.right, y.left
		y.operator = conflictCommuted(y.operator)
	}
	if !sameGroupExpression(x.left, y.left) {
		if sameGroupExpression(x.left, y.right) && sameGroupExpression(x.right, y.left) {
			y.left, y.right = y.right, y.left
			y.operator = conflictCommuted(y.operator)
		} else {
			return false, nil
		}
	}
	if x.left.ResultUsage().Collation.ID != y.left.ResultUsage().Collation.ID {
		return false, nil
	}
	// Only owned B-tree operator families admit order implications.
	family, ok := sqltype.DefaultIndexClass(x.left.ResultUsage().Type)
	if !ok {
		return false, nil
	}
	for _, expression := range [4]BoundExpr{x.left, x.right, y.left, y.right} {
		class, ok := sqltype.DefaultIndexClass(expression.ResultUsage().Type)
		if !ok || class.Family != family.Family {
			return false, nil
		}
		if _, err := sqltype.BindOrdering(expression.ResultUsage()); err != nil {
			return false, nil
		}
	}
	xi, yi := conflictOperatorIndex(x.operator), conflictOperatorIndex(y.operator)
	if xi < 0 || yi < 0 {
		return false, nil
	}
	if sameGroupExpression(x.right, y.right) {
		return conflictSameOperators[xi][yi], nil
	}
	first, ok := x.right.(literalExpression)
	if !ok {
		return false, nil
	}
	second, ok := y.right.(literalExpression)
	if !ok {
		return false, nil
	}
	if first.value.IsNull() {
		return true, nil
	}
	if second.value.IsNull() {
		return false, nil
	}
	op := conflictConstantOperators[xi][yi]
	if op == "" {
		return false, nil
	}
	provider, err := sqltype.BindComparison(second.value.Type(), first.value.Type())
	if err != nil || provider.SessionDependent() {
		return false, nil
	}
	sign, err := provider.Compare(ctx.Context, second.value, first.value, ctx.Input)
	if err != nil {
		return false, err
	}
	switch op {
	case "<":
		return sign < 0, nil
	case "<=":
		return sign <= 0, nil
	case "=":
		return sign == 0, nil
	case ">=":
		return sign >= 0, nil
	case ">":
		return sign > 0, nil
	case "<>":
		return sign != 0, nil
	}
	return false, nil
}
