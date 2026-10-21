package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Keep the frozen output layout while omitting only unreferenced payload
// demand. Clause/key stages and volatile outputs remain executable. nil uses
// means a full consumer (including materialized refresh); a nonnil empty list
// means only row presence is consumed. The stored definition is never changed.
func storedProjectionDemand(project *plan.ProjectNode, uses []int) (*plan.ProjectNode, error) {
	if uses == nil {
		return project, nil
	}
	for _, slot := range uses {
		if slot < 0 || slot >= len(project.Expressions) {
			return nil, fmt.Errorf("stuntgres: invalid stored projection use")
		}
	}
	var expressions []expr.Expr
	for slot, expression := range project.Expressions {
		if storedProjectionOutputDemand(expression, slot, uses) {
			continue
		}
		if expressions == nil {
			expressions = append([]expr.Expr(nil), project.Expressions...)
		}
		expressions[slot] = plan.LiteralExpr{Info: expr.InfoOf(expression), Value: sqltype.Null(expression.ResultUsage().Type)}
	}
	if expressions == nil {
		return project, nil
	}
	child, err := storedScheduledPayloadDemand(project.Child, func(visit func(expr.Expr)) {
		for _, expression := range expressions {
			visit(expression)
		}
	}, nil, nil)
	if err != nil {
		return nil, err
	}
	copy := *project
	copy.Expressions = expressions
	copy.Child = child
	return &copy, nil
}

func storedProjectionOutputDemand(expression expr.Expr, slot int, uses []int) bool {
	if uses == nil || expression.Properties().Volatility == expr.Volatile {
		return true
	}
	for _, use := range uses {
		if use == slot {
			return true
		}
	}
	return false
}

// Inspect the same retained payload/cache/clause authority without copying or
// executing the producer. Rule-program liveness converges before its sole
// copy-on-change projection pruning pass.
func storedProjectionDemandExpressions(project *plan.ProjectNode, uses []int, inspect func(expr.Expr), boundary func(plan.Node)) error {
	for _, slot := range uses {
		if slot < 0 || slot >= len(project.Expressions) {
			return fmt.Errorf("stuntgres: invalid stored projection use")
		}
	}
	retained := func(visit func(expr.Expr)) {
		for slot, expression := range project.Expressions {
			if storedProjectionOutputDemand(expression, slot, uses) {
				visit(expression)
			}
		}
	}
	retained(inspect)
	_, err := storedScheduledPayloadDemand(project.Child, retained, inspect, boundary)
	return err
}

// Projection scheduling owns payload cache slots on the outer query's unary
// path. Follow those existing owners, not source/derived-query frames, and
// retain the transitive slot reads of selected payloads and internal clauses.
func storedScheduledPayloadDemand(root plan.Node, retained func(func(expr.Expr)), inspect func(expr.Expr), boundary func(plan.Node)) (plan.Node, error) {
	input := func(node plan.Node) plan.Node {
		switch node.(type) {
		case *plan.ProjectNode, *plan.DerivedNode, *plan.WorktableScanNode, *plan.JoinNode, *plan.SetNode, *plan.RecursiveUnionNode:
			return nil
		}
		left, right := plan.QueryInputs(node)
		if right != nil {
			return nil
		}
		return left
	}
	var producers map[int]expr.Expr
	var owners map[plan.Node]int
	for node := root; node != nil; node = input(node) {
		var expressions []expr.Expr
		var base int
		switch n := node.(type) {
		case *plan.SortNode:
			expressions, base = n.Outputs, n.CacheWidth-len(n.Outputs)
		case *plan.EvaluationNode:
			expressions, base = n.Expressions, n.Base
		}
		if len(expressions) == 0 {
			continue
		}
		if producers == nil {
			producers = make(map[int]expr.Expr)
			owners = make(map[plan.Node]int)
		}
		owners[node] = base
		for i, expression := range expressions {
			producers[base+i] = expression
		}
	}
	if len(producers) == 0 && inspect == nil {
		return root, nil
	}
	var live map[int]bool
	if len(producers) != 0 {
		live = make(map[int]bool)
	}
	var demand func(expr.Expr)
	demand = func(expression expr.Expr) {
		if len(producers) == 0 {
			return
		}
		plan.WalkExpression(expression, func(node expr.Expr) {
			cache, ok := node.(plan.CachedOrderExpr)
			if !ok || live[cache.Slot] {
				return
			}
			live[cache.Slot] = true
			if producer := producers[cache.Slot]; producer != nil {
				demand(producer)
			}
		})
	}
	retained(demand)
	for slot, producer := range producers {
		if producer.Properties().Volatility == expr.Volatile {
			live[slot] = true
			demand(producer)
		}
	}
	clauses := func(visit func(expr.Expr), boundary func(plan.Node)) {
		for node := root; node != nil; node = input(node) {
			switch n := node.(type) {
			case *plan.SortNode:
				for _, key := range n.Keys {
					visit(key.Expression)
				}
			case *plan.EvaluationNode:
				// Payload reads are demanded through live owner slots only.
			case *plan.ProjectNode, *plan.DerivedNode, *plan.WorktableScanNode, *plan.JoinNode, *plan.SetNode, *plan.RecursiveUnionNode:
				if boundary != nil {
					boundary(node)
				}
			default:
				plan.QueryExpressions(node, visit)
			}
		}
	}
	clauses(demand, nil)
	if inspect != nil {
		for slot, expression := range producers {
			if live[slot] {
				inspect(expression)
			}
		}
		clauses(inspect, boundary)
		return root, nil
	}
	identity := func(expression expr.Expr) (expr.Expr, bool, error) {
		return expression, false, nil
	}
	var rewrite func(plan.Node) (plan.Node, error)
	rewrite = func(node plan.Node) (plan.Node, error) {
		base, owned := owners[node]
		if !owned {
			return node, nil
		}
		var expressions []expr.Expr
		var child plan.Node
		switch n := node.(type) {
		case *plan.SortNode:
			expressions, child = n.Outputs, n.Child
		case *plan.EvaluationNode:
			expressions, child = n.Expressions, n.Child
		}
		var replacement []expr.Expr
		for i, expression := range expressions {
			if live[base+i] {
				continue
			}
			if replacement == nil {
				replacement = append([]expr.Expr(nil), expressions...)
			}
			replacement[i] = plan.LiteralExpr{Info: expr.InfoOf(expression), Value: sqltype.Null(expression.ResultUsage().Type)}
		}
		if replacement == nil {
			return node, nil
		}
		next, err := plan.RewriteQueryExpressions(child, identity, rewrite)
		if err != nil {
			return nil, err
		}
		switch n := node.(type) {
		case *plan.SortNode:
			copy := *n
			copy.Child, copy.Outputs = next, replacement
			return &copy, nil
		case *plan.EvaluationNode:
			copy := *n
			copy.Child, copy.Expressions = next, replacement
			return &copy, nil
		}
		return node, nil
	}
	return plan.RewriteQueryExpressions(root, identity, rewrite)
}
