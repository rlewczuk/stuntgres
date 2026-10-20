package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This consumer deliberately does not change SELECT's exact-conjunction mask.
// It proves SQL TRUE => TRUE with the owned immutable comparison providers.
func conflictPredicateImpliedBy(ctx *EvalContext, a, b expr.Expr) (bool, error) {
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
func normalizeConflictPredicate(ctx *EvalContext, node expr.Expr) (expr.Expr, error) {
	if node == nil {
		return nil, nil
	}
	p := node.Properties()
	if !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion && plannedVolatility(node) == expr.Immutable {
		value, err := node.Eval(ctx)
		if err != nil {
			return nil, err
		}
		info := expr.InfoOf(node)
		info.Property = expr.Properties{}
		return literalExpression{Info: info, Value: value}, nil
	}
	switch n := node.(type) {
	case logicalExpression:
		left, err := normalizeConflictPredicate(ctx, n.Left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.Right)
		if err != nil {
			return nil, err
		}
		n.Left, n.Right = left, right
		if n.Op == parser.LogicalNot {
			return negateConflictPredicate(n), nil
		}
		if truth, ok := conflictLiteralTruth(left); ok {
			if n.Op == parser.LogicalAnd && truth == True || n.Op == parser.LogicalOr && truth == False {
				return right, nil
			}
			if n.Op == parser.LogicalAnd && truth == False || n.Op == parser.LogicalOr && truth == True {
				return left, nil
			}
		}
		if truth, ok := conflictLiteralTruth(right); ok {
			if n.Op == parser.LogicalAnd && truth == True || n.Op == parser.LogicalOr && truth == False {
				return left, nil
			}
			if n.Op == parser.LogicalAnd && truth == False || n.Op == parser.LogicalOr && truth == True {
				return right, nil
			}
		}
		return n, nil
	case comparisonExpression:
		left, err := normalizeConflictPredicate(ctx, n.Left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.Right)
		if err != nil {
			return nil, err
		}
		n.Left, n.Right = left, right
		if n.Operator == "=" && left.ResultUsage().Type.ID() == sqltype.Bool {
			if truth, ok := conflictLiteralTruth(right); ok {
				if truth == True {
					return left, nil
				}
				if truth == False {
					return negateConflictPredicate(logicalExpression{Info: n.Info, Op: parser.LogicalNot, Left: left}), nil
				}
			}
			if truth, ok := conflictLiteralTruth(left); ok {
				if truth == True {
					return right, nil
				}
				if truth == False {
					return negateConflictPredicate(logicalExpression{Info: n.Info, Op: parser.LogicalNot, Left: right}), nil
				}
			}
		}
		return n, nil
	case selectedOperatorExpression:
		var err error
		n.Provider, err = normalizeConflictPredicate(ctx, n.Provider)
		return n, err
	case arithmeticExpression:
		left, err := normalizeConflictPredicate(ctx, n.Left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.Right)
		n.Left, n.Right = left, right
		return n, err
	case nullTestExpression:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case truthTestExpression:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case coercionExpression:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case collationExpression:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	}
	return node, nil
}
func conflictLiteralTruth(node expr.Expr) (Truth, bool) {
	literal, ok := node.(literalExpression)
	if !ok || literal.Value.Type() == nil || literal.Value.Type().ID() != sqltype.Bool {
		return Unknown, false
	}
	truth, err := TruthOf(literal.Value)
	return truth, err == nil
}
func negateConflictPredicate(n logicalExpression) expr.Expr {
	switch child := n.Left.(type) {
	case logicalExpression:
		if child.Op == parser.LogicalNot {
			return child.Left
		}
		if child.Op == parser.LogicalAnd || child.Op == parser.LogicalOr {
			op := parser.LogicalAnd
			if child.Op == parser.LogicalAnd {
				op = parser.LogicalOr
			}
			return logicalExpression{Info: n.Info, Op: op, Left: negateConflictPredicate(logicalExpression{Info: n.Info, Op: parser.LogicalNot, Left: child.Left}), Right: negateConflictPredicate(logicalExpression{Info: n.Info, Op: parser.LogicalNot, Left: child.Right})}
		}
	case nullTestExpression:
		child.Not = !child.Not
		return child
	}
	return n
}
func conflictPredicateProof(ctx *EvalContext, a, b expr.Expr) (bool, error) {
	if plannedVolatility(a) != expr.Immutable || plannedVolatility(b) != expr.Immutable {
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
	if right, ok := b.(logicalExpression); ok && right.Op == parser.LogicalAnd {
		left, err := conflictPredicateProof(ctx, a, right.Left)
		if err != nil || !left {
			return left, err
		}
		return conflictPredicateProof(ctx, a, right.Right)
	}
	if left, ok := a.(logicalExpression); ok && left.Op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, left.Left, b)
		if err != nil || !first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.Right, b)
	}
	if right, ok := b.(logicalExpression); ok && right.Op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, a, right.Left)
		if err != nil || first {
			return first, err
		}
		second, err := conflictPredicateProof(ctx, a, right.Right)
		if err != nil || second {
			return second, err
		}
	}
	if left, ok := a.(logicalExpression); ok && left.Op == parser.LogicalAnd {
		first, err := conflictPredicateProof(ctx, left.Left, b)
		if err != nil || first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.Right, b)
	}
	if required, ok := b.(nullTestExpression); ok && required.Not {
		return conflictStrictFor(a, required.Child), nil
	}
	return conflictComparisonProof(ctx, a, b)
}
func conflictStrictFor(node, subject expr.Expr) bool {
	if sameGroupExpression(node, subject) {
		return node.ResultUsage().Type.ID() == sqltype.Bool
	}
	if c, ok := node.(comparisonExpression); ok {
		return !c.Predicate.SessionDependent() && (sameGroupExpression(c.Left, subject) || sameGroupExpression(c.Right, subject))
	}
	if n, ok := node.(logicalExpression); ok && n.Op == parser.LogicalNot {
		return conflictStrictFor(n.Left, subject)
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
func conflictProofComparison(node expr.Expr) (comparisonExpression, bool) {
	if comparison, ok := node.(comparisonExpression); ok {
		return comparison, true
	}
	not, ok := node.(logicalExpression)
	if !ok || not.Op != parser.LogicalNot {
		return comparisonExpression{}, false
	}
	comparison, ok := not.Left.(comparisonExpression)
	if !ok {
		return comparisonExpression{}, false
	}
	switch comparison.Operator {
	case "<":
		comparison.Operator = ">="
	case "<=":
		comparison.Operator = ">"
	case "=":
		comparison.Operator = "<>"
	case ">=":
		comparison.Operator = "<"
	case ">":
		comparison.Operator = "<="
	case "<>", "!=":
		comparison.Operator = "="
	default:
		return comparisonExpression{}, false
	}
	return comparison, true
}
func conflictComparisonProof(ctx *EvalContext, a, b expr.Expr) (bool, error) {
	x, ok := conflictProofComparison(a)
	if !ok {
		return false, nil
	}
	y, ok := conflictProofComparison(b)
	if !ok {
		return false, nil
	}
	if x.Predicate.SessionDependent() || y.Predicate.SessionDependent() || x.Namespace != y.Namespace {
		return false, nil
	}
	if _, ok := x.Left.(literalExpression); ok {
		x.Left, x.Right = x.Right, x.Left
		x.Operator = conflictCommuted(x.Operator)
	}
	if _, ok := y.Left.(literalExpression); ok {
		y.Left, y.Right = y.Right, y.Left
		y.Operator = conflictCommuted(y.Operator)
	}
	if !sameGroupExpression(x.Left, y.Left) {
		if sameGroupExpression(x.Left, y.Right) && sameGroupExpression(x.Right, y.Left) {
			y.Left, y.Right = y.Right, y.Left
			y.Operator = conflictCommuted(y.Operator)
		} else {
			return false, nil
		}
	}
	xRef, xConflict := x.Predicate.InputCollation()
	yRef, yConflict := y.Predicate.InputCollation()
	if xConflict || yConflict || xRef != yRef {
		return false, nil
	}
	// Only owned B-tree operator families admit order implications.
	family, ok := sqltype.DefaultIndexClass(x.Left.ResultUsage().Type)
	if !ok {
		return false, nil
	}
	for _, expression := range [4]expr.Expr{x.Left, x.Right, y.Left, y.Right} {
		class, ok := sqltype.DefaultIndexClass(expression.ResultUsage().Type)
		if !ok || class.Family != family.Family {
			return false, nil
		}
		if _, err := sqltype.BindOrdering(expression.ResultUsage()); err != nil {
			return false, nil
		}
	}
	xi, yi := conflictOperatorIndex(x.Operator), conflictOperatorIndex(y.Operator)
	if xi < 0 || yi < 0 {
		return false, nil
	}
	if sameGroupExpression(x.Right, y.Right) {
		return conflictSameOperators[xi][yi], nil
	}
	first, ok := x.Right.(literalExpression)
	if !ok {
		return false, nil
	}
	second, ok := y.Right.(literalExpression)
	if !ok {
		return false, nil
	}
	if first.Value.IsNull() {
		return true, nil
	}
	if second.Value.IsNull() {
		return false, nil
	}
	op := conflictConstantOperators[xi][yi]
	if op == "" {
		return false, nil
	}
	provider, err := sqltype.BindComparison(second.Value.Type(), first.Value.Type(), xRef, false)
	if err != nil || provider.SessionDependent() {
		return false, nil
	}
	sign, err := provider.Compare(ctx.Context, second.Value, first.Value, ctx.Input)
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
