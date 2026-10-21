package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Only admitted, analyzed nodes participate. A scalar subquery is a separate
// query boundary and must not be mistaken for a top-level aggregate/window.
func viewWriteShape(d *catalog.Relation) (*plan.ProjectNode, *plan.SourceNode, []expr.Expr, string) {
	q := viewOf(d).syntax
	if q.Distinct != nil {
		return nil, nil, nil, "Views containing DISTINCT are not automatically updatable."
	}
	if q.GroupBy != nil {
		return nil, nil, nil, "Views containing GROUP BY are not automatically updatable."
	}
	if q.Having != nil {
		return nil, nil, nil, "Views containing HAVING are not automatically updatable."
	}
	if q.Set != nil {
		return nil, nil, nil, "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	}
	if q.Bounds != nil {
		return nil, nil, nil, "Views containing LIMIT or OFFSET are not automatically updatable."
	}
	p, ok := viewOf(d).query.Root.(*plan.ProjectNode)
	if !ok {
		return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
	}
	var predicates []expr.Expr
	hasTargetSet := p.HasTargetSet
	node := p.Child
	for {
		switch n := node.(type) {
		case *plan.SetProjectNode:
			hasTargetSet = true
			node = n.Child
		case *plan.AggregateNode:
			return nil, nil, nil, "Views that return aggregate functions are not automatically updatable."
		case *plan.WindowNode:
			return nil, nil, nil, "Views that return window functions are not automatically updatable."
		case *plan.SortNode:
			node = n.Child
		case *plan.RowLockNode:
			node = n.Child
		case *plan.FilterNode:
			predicates = append(predicates, n.Predicate)
			node = n.Child
		case *plan.SourceNode:
			if hasTargetSet {
				return nil, nil, nil, "Views that return set-returning functions are not automatically updatable."
			}
			// Frozen-layout dump replay wraps ordinary sources in aliases.
			from := q.From
			for {
				alias, ok := from.(*parser.AliasReference)
				if !ok {
					break
				}
				from = alias.Input
			}
			if _, ordinary := from.(*parser.TableReference); !ordinary {
				return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
			}
			return p, n, predicates, ""
		default:
			if hasTargetSet {
				return nil, nil, nil, "Views that return set-returning functions are not automatically updatable."
			}
			return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
		}
	}
}

func viewDirectColumn(expr expr.Expr) (plan.ColumnExpr, bool) {
	if c, ok := expr.(plan.ColumnExpr); ok {
		return c, true
	}
	if c, ok := expr.(plan.CoercionExpr); ok && c.ResultUsage() == c.Child.ResultUsage() {
		return viewDirectColumn(c.Child)
	}
	return plan.ColumnExpr{}, false
}

func sameViewRelation(a, b ids.ObjectRef) bool {
	a.Address.SubID = 0
	b.Address.SubID = 0
	return a == b
}
