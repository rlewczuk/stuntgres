package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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
	if !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion && PlannedVolatility(node) == expr.Immutable {
		value, err := node.Eval(ctx)
		if err != nil {
			return nil, err
		}
		info := expr.InfoOf(node)
		info.Property = expr.Properties{}
		return plan.LiteralExpr{Info: info, Value: value}, nil
	}
	switch n := node.(type) {
	case plan.LogicalExpr:
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
			if n.Op == parser.LogicalAnd && truth == plan.True || n.Op == parser.LogicalOr && truth == plan.False {
				return right, nil
			}
			if n.Op == parser.LogicalAnd && truth == plan.False || n.Op == parser.LogicalOr && truth == plan.True {
				return left, nil
			}
		}
		if truth, ok := conflictLiteralTruth(right); ok {
			if n.Op == parser.LogicalAnd && truth == plan.True || n.Op == parser.LogicalOr && truth == plan.False {
				return left, nil
			}
			if n.Op == parser.LogicalAnd && truth == plan.False || n.Op == parser.LogicalOr && truth == plan.True {
				return right, nil
			}
		}
		return n, nil
	case plan.ComparisonExpr:
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
				if truth == plan.True {
					return left, nil
				}
				if truth == plan.False {
					return negateConflictPredicate(plan.LogicalExpr{Info: n.Info, Op: parser.LogicalNot, Left: left}), nil
				}
			}
			if truth, ok := conflictLiteralTruth(left); ok {
				if truth == plan.True {
					return right, nil
				}
				if truth == plan.False {
					return negateConflictPredicate(plan.LogicalExpr{Info: n.Info, Op: parser.LogicalNot, Left: right}), nil
				}
			}
		}
		return n, nil
	case plan.SelectedOperatorExpr:
		var err error
		n.Provider, err = normalizeConflictPredicate(ctx, n.Provider)
		return n, err
	case plan.ArithmeticExpr:
		left, err := normalizeConflictPredicate(ctx, n.Left)
		if err != nil {
			return nil, err
		}
		right, err := normalizeConflictPredicate(ctx, n.Right)
		n.Left, n.Right = left, right
		return n, err
	case plan.NullTestExpr:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case plan.TruthTestExpr:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case plan.CoercionExpr:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	case plan.CollationExpr:
		child, err := normalizeConflictPredicate(ctx, n.Child)
		n.Child = child
		return n, err
	}
	return node, nil
}
func conflictLiteralTruth(node expr.Expr) (plan.Truth, bool) {
	literal, ok := node.(plan.LiteralExpr)
	if !ok || literal.Value.Type() == nil || literal.Value.Type().ID() != sqltype.Bool {
		return plan.Unknown, false
	}
	truth, err := plan.TruthOf(literal.Value)
	return truth, err == nil
}
func negateConflictPredicate(n plan.LogicalExpr) expr.Expr {
	switch child := n.Left.(type) {
	case plan.LogicalExpr:
		if child.Op == parser.LogicalNot {
			return child.Left
		}
		if child.Op == parser.LogicalAnd || child.Op == parser.LogicalOr {
			op := parser.LogicalAnd
			if child.Op == parser.LogicalAnd {
				op = parser.LogicalOr
			}
			return plan.LogicalExpr{Info: n.Info, Op: op, Left: negateConflictPredicate(plan.LogicalExpr{Info: n.Info, Op: parser.LogicalNot, Left: child.Left}), Right: negateConflictPredicate(plan.LogicalExpr{Info: n.Info, Op: parser.LogicalNot, Left: child.Right})}
		}
	case plan.NullTestExpr:
		child.Not = !child.Not
		return child
	}
	return n
}
func conflictPredicateProof(ctx *EvalContext, a, b expr.Expr) (bool, error) {
	if PlannedVolatility(a) != expr.Immutable || PlannedVolatility(b) != expr.Immutable {
		return false, nil
	}
	if truth, ok := conflictLiteralTruth(b); ok && truth == plan.True {
		return true, nil
	}
	if truth, ok := conflictLiteralTruth(a); ok && truth != plan.True {
		return true, nil
	}
	if sameGroupExpression(a, b) {
		return true, nil
	}
	if right, ok := b.(plan.LogicalExpr); ok && right.Op == parser.LogicalAnd {
		left, err := conflictPredicateProof(ctx, a, right.Left)
		if err != nil || !left {
			return left, err
		}
		return conflictPredicateProof(ctx, a, right.Right)
	}
	if left, ok := a.(plan.LogicalExpr); ok && left.Op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, left.Left, b)
		if err != nil || !first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.Right, b)
	}
	if right, ok := b.(plan.LogicalExpr); ok && right.Op == parser.LogicalOr {
		first, err := conflictPredicateProof(ctx, a, right.Left)
		if err != nil || first {
			return first, err
		}
		second, err := conflictPredicateProof(ctx, a, right.Right)
		if err != nil || second {
			return second, err
		}
	}
	if left, ok := a.(plan.LogicalExpr); ok && left.Op == parser.LogicalAnd {
		first, err := conflictPredicateProof(ctx, left.Left, b)
		if err != nil || first {
			return first, err
		}
		return conflictPredicateProof(ctx, left.Right, b)
	}
	if required, ok := b.(plan.NullTestExpr); ok && required.Not {
		return conflictStrictFor(a, required.Child), nil
	}
	return conflictComparisonProof(ctx, a, b)
}
func conflictStrictFor(node, subject expr.Expr) bool {
	if sameGroupExpression(node, subject) {
		return node.ResultUsage().Type.ID() == sqltype.Bool
	}
	if c, ok := node.(plan.ComparisonExpr); ok {
		return !c.Predicate.SessionDependent() && (sameGroupExpression(c.Left, subject) || sameGroupExpression(c.Right, subject))
	}
	if n, ok := node.(plan.LogicalExpr); ok && n.Op == parser.LogicalNot {
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
func conflictProofComparison(node expr.Expr) (plan.ComparisonExpr, bool) {
	if comparison, ok := node.(plan.ComparisonExpr); ok {
		return comparison, true
	}
	not, ok := node.(plan.LogicalExpr)
	if !ok || not.Op != parser.LogicalNot {
		return plan.ComparisonExpr{}, false
	}
	comparison, ok := not.Left.(plan.ComparisonExpr)
	if !ok {
		return plan.ComparisonExpr{}, false
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
		return plan.ComparisonExpr{}, false
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
	if _, ok := x.Left.(plan.LiteralExpr); ok {
		x.Left, x.Right = x.Right, x.Left
		x.Operator = conflictCommuted(x.Operator)
	}
	if _, ok := y.Left.(plan.LiteralExpr); ok {
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
	first, ok := x.Right.(plan.LiteralExpr)
	if !ok {
		return false, nil
	}
	second, ok := y.Right.(plan.LiteralExpr)
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
