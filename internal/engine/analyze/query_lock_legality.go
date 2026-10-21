package analyze

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
)

// QueryRootRowMode is the first row lock mode of a query root, or 0.
func QueryRootRowMode(node plan.Node) lock.Mode {
	for {
		switch n := node.(type) {
		case *plan.ProjectNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.RowLockNode:
			return n.FirstMode
		case *plan.DistinctNode, *plan.DistinctOnNode, *plan.AggregateNode:
			return 0 // Descendant marks are consumed at materialization.
		default:
			return 0
		}
	}
}

// Only the stored query's output boundary stops inherited reference-kind
// context. Ordinary joins/derived queries retain their existing legality and
// sibling checks; a nested stored set view applies this boundary on its entry.
func QueryHasSetOutput(node plan.Node) bool {
	for {
		switch n := node.(type) {
		case *plan.ProjectNode:
			node = n.Child
		case *plan.SortNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.FilterNode:
			node = n.Child
		case *plan.SetNode, *plan.RecursiveUnionNode:
			return true
		default:
			return false
		}
	}
}

// QueryRowLockLegality rejects a row lock mode on a query whose shape
// PostgreSQL does not lock (sets, DISTINCT, grouping, aggregates, windows,
// target-list set-returning functions), recursing into its FROM queries.
func QueryRowLockLegality(root plan.Node, mode lock.Mode) error {
	if mode == 0 {
		return nil
	}
	project, ok := root.(*plan.ProjectNode)
	if !ok || project.Syntax == nil {
		return fmt.Errorf("stuntgres: missing rowmark query syntax")
	}
	suffix := ""
	switch {
	case project.Syntax.Set != nil:
		suffix = "UNION/INTERSECT/EXCEPT"
	case project.Syntax.Distinct != nil:
		suffix = "DISTINCT clause"
	case project.Syntax.GroupBy != nil:
		suffix = "GROUP BY clause"
	case project.Syntax.Having != nil:
		suffix = "HAVING clause"
	default:
		for node := project.Child; node != nil; {
			switch n := node.(type) {
			case *plan.ProjectNode:
				node = n.Child
			case *plan.SetProjectNode:
				node = n.Child
			case *plan.LimitNode:
				node = n.Child
			case *plan.EvaluationNode:
				node = n.Child
			case *plan.SortNode:
				node = n.Child
			case *plan.FilterNode:
				node = n.Child
			case *plan.RowLockNode:
				node = n.Child
			case *plan.AggregateNode:
				suffix = "aggregate functions"
				node = nil
			case *plan.WindowNode:
				suffix = "window functions"
				node = nil
			case *plan.DistinctNode, *plan.DistinctOnNode:
				suffix = "DISTINCT clause"
				node = nil
			default:
				node = nil
			}
		}
	}
	if suffix == "" && project.HasTargetSet && stringInheritedRowMark(ViewFromRoot(project)) {
		suffix = "set-returning functions in the target list"
	}
	if suffix != "" {
		return sqlError("0A000", RowLockClause(mode)+" is not allowed with "+suffix)
	}
	var from func(plan.Node) error
	from = func(node plan.Node) error {
		switch n := node.(type) {
		case *plan.DerivedNode:
			if n.CTE != nil && n.CTE.Mutation != nil {
				return nil
			}
			if n.CTE != nil {
				return QueryRowLockLegality(n.Query.Root, QueryRootRowMode(n.Query.Root))
			}
			return QueryRowLockLegality(n.Query.Root, exec.StrongerQueryRowMode(mode, QueryRootRowMode(n.Query.Root)))
		case *plan.JoinNode:
			if err := from(n.Left); err != nil {
				return err
			}
			return from(n.Right)
		}
		return nil
	}
	return from(ViewFromRoot(project))
}

// View lock pushdown marks relation/subquery RTEs, not computed function rows.
func stringInheritedRowMark(node plan.Node) bool {
	switch n := node.(type) {
	case *plan.SourceNode:
		return true
	case *plan.DerivedNode:
		return n.CTE == nil
	case *plan.JoinNode:
		return stringInheritedRowMark(n.Left) || stringInheritedRowMark(n.Right)
	}
	return false
}

// Inherited marks follow physical lineage, unlike general source/dependency
// traversal: a materialized set still reads both arms but exposes neither's rows.
func QueryRowMarkReachesSource(node plan.Node, occurrence int) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*plan.SetNode); ok {
		return false
	}
	if source, ok := node.(*plan.SourceNode); ok {
		return source.Occurrence == occurrence
	}
	left, right := plan.QueryInputs(node)
	return QueryRowMarkReachesSource(left, occurrence) || QueryRowMarkReachesSource(right, occurrence)
}

// QueryOccurrenceMark merges the inherited mark with an occurrence's own mark.
func QueryOccurrenceMark(marks []plan.RowMark, occurrence int, inherited plan.RowMark) plan.RowMark {
	if occurrence < len(marks) {
		return exec.MergeQueryRowMark(inherited, marks[occurrence])
	}
	return inherited
}
