package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Query boundaries are explicit: lexical argument walking visits predicate
// left operands but never confuses a child query's local columns with ours.
func WalkExpression(node expr.Expr, visit func(expr.Expr)) {
	expr.Walk(node, ExpressionChildren, visit)
}

// Enumerate lexical children only; a subquery's query graph is a separate frame.
func ExpressionChildren(node expr.Expr, walk func(expr.Expr)) {
	args := func(children []expr.Expr) {
		for _, child := range children {
			walk(child)
		}
	}
	switch e := node.(type) {
	case SelectedOperatorExpr:
		walk(e.Provider)
	case UpdateOutputExpr:
		// The first assignment owns the query; later tuple outputs are leaves.
	case EarthCheckExpr:
		walk(e.Value)
	case DomainValueExpr:
		// Conversion-local VALUE is a lexical leaf, never a query column.
	case AssignmentValueExpr:
		walk(e.Base)
		walk(e.Value)
		for target := e.Target; target != nil; target = target.Next {
			for _, sub := range target.Node.Subscripts {
				walk(sub.Lower)
				walk(sub.Upper)
			}
		}
	case ViewQualificationExpr:
		args(e.Predicates)
		walk(e.User)
	case GroupingExpr:
		args(e.Args)
	case RowExpr:
		args(e.Fields)
	case FieldExpr:
		walk(e.Base)
	case ArrayExpr:
		args(e.Elements)
	case SubscriptExpr:
		walk(e.Base)
		for _, s := range e.Subscripts {
			walk(s.Lower)
			walk(s.Upper)
		}
	case ReturningColumnExpr:
		if e.Value != nil {
			walk(e.Value)
		} else {
			walk(e.ColumnExpr)
		}
	case ReturningWholeRowExpr:
		args(e.Members)
	case MergeActionExpr:
		// A lexical owner leaf has no expression children.
	case CoercionExpr:
		walk(e.Child)
	case CollationExpr:
		walk(e.Child)
	case ArithmeticExpr:
		walk(e.Left)
		walk(e.Right)
	case BitwiseExpr:
		walk(e.Left)
		walk(e.Right)
	case RowComparisonExpr:
		for _, pair := range e.Comparisons {
			walk(pair.Left)
			walk(pair.Right)
		}
	case ScalarArrayComparisonExpr:
		walk(e.Left)
		if e.Array != nil {
			walk(e.Array)
		}
		args(e.Items)
	case ComparisonExpr:
		walk(e.Left)
		walk(e.Right)
	case LikeExpr:
		walk(e.Left)
		walk(e.Right)
	case LogicalExpr:
		walk(e.Left)
		walk(e.Right)
	case DistinctExpr:
		walk(e.Left)
		walk(e.Right)
	case NullTestExpr:
		walk(e.Child)
	case TruthTestExpr:
		walk(e.Child)
	case ConditionalExpr:
		args(e.Args)
	case InstalledUUIDOsspExpr:
		// No argument children.
	case InstalledTextExpr:
		walk(e.Arg)
	case ExtensionPathsExpr:
		walk(e.Arg)
	case CaseExpr:
		walk(e.Test)
		for _, arm := range e.Arms {
			walk(arm.Condition)
			walk(arm.Result)
		}
		walk(e.Otherwise)
	case SubqueryExpr:
		for _, comparison := range e.Comparisons {
			walk(comparison.Left)
		}
	case functions.Timezone:
		// Known divergence kept from before the node protocol: PostgreSQL walks
		// timezone(zone, value) zone first, which decides the reported column in
		// GROUP BY and nested-aggregate errors. Children follow PostgreSQL.
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case expr.Composite:
		// Last, so the explicit cases above keep precedence over embedded nodes.
		args(e.Children())
	}
}

// Searched CASE has a physical-literal decision requiring no expression,
// conversion, provider or domain evaluation. Demand and simplification share it.
func LiteralSearchedCaseMatch(condition expr.Expr) (known, match bool, err error) {
	literal, ok := condition.(LiteralExpr)
	if !ok {
		return false, false, nil
	}
	if literal.Value.IsNull() {
		return true, false, nil
	}
	match, err = literal.Value.Boolean()
	return err == nil, match, err
}

// This walk discovers payload reads without advancing any scalar preflight
// frontier. Unknown branches retain conservative demand; only the canonical
// physical literal policies exclude children.
func WalkDemandedExpression(node expr.Expr, visit func(expr.Expr)) {
	if node == nil {
		return
	}
	visit(node)
	walk := func(child expr.Expr) { WalkDemandedExpression(child, visit) }
	switch e := node.(type) {
	case CaseExpr:
		if e.Test != nil {
			ExpressionChildren(node, walk)
			return
		}
		for _, arm := range e.Arms {
			walk(arm.Condition)
			known, match, err := LiteralSearchedCaseMatch(arm.Condition)
			if err != nil || !known {
				walk(arm.Result)
			} else if match {
				walk(arm.Result)
				return
			}
		}
		walk(e.Otherwise)
	case ConditionalExpr:
		if e.Kind != parser.Coalesce {
			ExpressionChildren(node, walk)
			return
		}
		for _, arg := range e.Args {
			walk(arg)
			if literal, ok := arg.(LiteralExpr); ok && !literal.Value.IsNull() {
				return
			}
		}
	case LogicalExpr:
		walk(e.Left)
		if _, decisive, err := DecisiveLogicalValue(&e); err != nil || !decisive {
			walk(e.Right)
		}
	default:
		ExpressionChildren(node, walk)
	}
}
