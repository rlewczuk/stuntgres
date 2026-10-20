package engine

import (
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Demand is derived from typed NEW references before substitution. The ordinary
// stored projection planner also retains volatile outputs and clause caches.
func ruleProducerOutputUses(original binding, rule ruleDefinition, action binding) []int {
	uses := []int{}
	inspect := func(bound expr.Expr) {
		walkBoundExpression(bound, func(node expr.Expr) {
			column, ok := node.(plan.ColumnExpr)
			if !ok || column.Scope != rulePseudoScope || column.Frame != 2 {
				return
			}
			target := slices.Index(original.relation.Attributes, column.Origin.Address.SubID)
			for output, slot := range original.targets {
				if slot == target && !slices.Contains(uses, output) {
					uses = append(uses, output)
				}
			}
		})
	}
	query := func(root plan.Node) { visitCTEQuery(root, func(plan.Node) {}, inspect, true) }
	expression := func(bound expr.Expr) {
		if bound != nil {
			query(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	targets := func(items []*plan.ArrayAssignmentTarget) {
		for _, target := range items {
			for ; target != nil; target = target.Next {
				for _, sub := range target.Node.Subscripts {
					expression(sub.Lower)
					expression(sub.Upper)
				}
			}
		}
	}
	expression(rule.condition)
	if rule.conditionBinding != nil && rule.conditionBinding.query != nil {
		query(rule.conditionBinding.query.Root)
	}
	if action.query != nil {
		query(action.query.Root)
	}
	for _, row := range action.expressions {
		for _, expr := range row {
			expression(expr)
		}
	}
	expression(action.predicate)
	query(action.delete.using)
	targets(action.arrayTargets)
	if action.update != nil {
		query(action.update.from)
		expression(action.update.predicate)
		for _, expr := range action.update.assignments {
			expression(expr)
		}
		targets(action.update.arrayTargets)
	}
	if action.returning != nil {
		for _, expr := range action.returning.expressions {
			expression(expr)
		}
	}
	if action.insert.conflict != nil {
		expression(action.insert.conflict.predicate)
		for _, key := range action.insert.conflict.keys {
			expression(key.expression)
		}
		if action.insert.conflict.update != nil {
			expression(action.insert.conflict.update.predicate)
			for _, expr := range action.insert.conflict.update.assignments {
				expression(expr)
			}
			targets(action.insert.conflict.update.arrayTargets)
		}
	}
	return uses
}

// Apply ordinary projection liveness at each derived producer boundary before
// rule-program static preparation. Slots are interpreted in their retained
// lexical input frame, including the offset contributed by preceding join arms.
// Analysis-only expressions are not executable demand.
func ruleQueryProducerDemand(root plan.Node, external func(func(expr.Expr)), targetPrefix int) (plan.Node, error) {
	type producer struct {
		frame   uint32
		offset  int
		demand  []bool
		project *plan.ProjectNode
		uses    []int
		active  bool
		dirty   bool
	}
	var producers []*producer
	bySource := make(map[plan.Node]*producer)
	var collect func(plan.Node, int, uint32, uint32)
	collect = func(node plan.Node, offset int, exposedFrame, inputFrame uint32) {
		if node == nil {
			return
		}
		if derived, ok := node.(*plan.DerivedNode); ok {
			if bySource[derived] != nil || derived.Query == nil {
				return
			}
			project, ok := derived.Query.Root.(*plan.ProjectNode)
			if !ok {
				return
			}
			frame := derived.ScanFrame
			if frame == 0 {
				// Synthetic producers inherit their consumer's current frame;
				// zero is not a separate lexical namespace.
				frame = inputFrame
			}
			item := &producer{frame: frame, offset: offset, demand: derived.Demand, project: project, uses: []int{}}
			bySource[derived] = item
			producers = append(producers, item)
			// openDerivedNode creates a fresh inner query state. Its project
			// supplies an explicit frame or uses the ordinary default frame.
			collect(project, 0, 0, 1)
			return
		}
		if project, projected := node.(*plan.ProjectNode); projected {
			if exposedFrame != 0 && bySource[project] == nil {
				// A join arm consumes this projection's output in the enclosing
				// frame; its payload reads its own local input frame.
				item := &producer{frame: exposedFrame, offset: offset, project: project, uses: []int{}}
				bySource[project] = item
				producers = append(producers, item)
			}
			// A projection's child owns a local input frame, even when the
			// projected output follows another arm in a correlated join.
			offset = 0
			if project.Frame != 0 {
				inputFrame = project.Frame
			}
		}
		left, right := queryInputs(node)
		if join, joined := node.(*plan.JoinNode); joined {
			frame := inputFrame
			if join.Frame != 0 {
				frame = join.Frame
			}
			collect(left, offset, frame, frame)
			collect(right, offset+len(left.Schema()), frame, frame)
		} else {
			collect(left, offset, 0, inputFrame)
			collect(right, offset, 0, inputFrame)
		}
		queryExpressions(node, func(expression expr.Expr) {
			walkDemandedBoundExpression(expression, func(node expr.Expr) {
				if subquery, ok := node.(plan.SubqueryExpr); ok && !subquery.CTEEdge && subquery.QueryPlan != nil {
					collect(subquery.QueryPlan.Root, 0, 0, 1)
				}
			})
		})
	}
	collect(root, 0, 0, 1)
	if len(producers) == 0 {
		return root, nil
	}
	var queue []*producer
	enqueue := func(item *producer) {
		if !item.dirty {
			item.dirty = true
			queue = append(queue, item)
		}
	}
	activate := func(item *producer) {
		if item == nil || item.active {
			return
		}
		item.active = true
		for slot, used := range item.demand {
			if used {
				item.uses = append(item.uses, slot)
			}
		}
		enqueue(item)
	}
	var failure error
	var observe func(plan.Node)
	var inspect func(expr.Expr, int)
	inspect = func(expression expr.Expr, prefix int) {
		walkDemandedBoundExpression(expression, func(node expr.Expr) {
			if column, ok := node.(plan.ColumnExpr); ok {
				for _, item := range producers {
					slot := column.Slot - prefix - item.offset
					if column.Frame != item.frame || slot < 0 || slot >= len(item.project.Expressions) {
						continue
					}
					activate(item)
					if !slices.Contains(item.uses, slot) {
						item.uses = append(item.uses, slot)
						enqueue(item)
					}
				}
			}
			if subquery, ok := node.(plan.SubqueryExpr); ok && !subquery.CTEEdge && subquery.QueryPlan != nil {
				observe(subquery.QueryPlan.Root)
			}
		})
	}
	observe = func(node plan.Node) {
		if node == nil || failure != nil {
			return
		}
		if item := bySource[node]; item != nil {
			activate(item)
			return
		}
		switch n := node.(type) {
		case *plan.DerivedNode:
			// Modifying scans have no SELECT graph and retain their spool key.
			return
		case *plan.ProjectNode:
			failure = storedProjectionDemandExpressions(n, nil, func(expression expr.Expr) {
				inspect(expression, 0)
			}, observe)
		default:
			queryExpressions(node, func(expression expr.Expr) { inspect(expression, 0) })
			left, right := queryInputs(node)
			observe(left)
			observe(right)
		}
	}
	observe(root)
	if external != nil {
		external(func(expression expr.Expr) { inspect(expression, targetPrefix) })
	}
	// Original immutable recipes remain intact until liveness converges. A
	// retained payload can read a supplier in an outer lexical frame; each new
	// output use enqueues that supplier, never its executed rows or invocation.
	for next := 0; next < len(queue) && failure == nil; next++ {
		item := queue[next]
		item.dirty = false
		failure = storedProjectionDemandExpressions(item.project, item.uses, func(expression expr.Expr) {
			inspect(expression, 0)
		}, observe)
	}
	if failure != nil {
		return nil, failure
	}
	uses := make(map[*plan.ProjectNode][]int)
	for _, item := range producers {
		if !item.active {
			continue
		}
		current, found := uses[item.project]
		if !found {
			current = []int{}
		}
		for _, slot := range item.uses {
			if !slices.Contains(current, slot) {
				current = append(current, slot)
			}
		}
		uses[item.project] = current
	}
	pruned := make(map[*plan.ProjectNode]*plan.ProjectNode)
	for project, outputUses := range uses {
		demanded, err := storedProjectionDemand(project, outputUses)
		if err != nil {
			return nil, err
		}
		if demanded != project {
			pruned[project] = demanded
		}
	}
	if len(pruned) == 0 {
		return root, nil
	}
	identity := func(expression expr.Expr) (expr.Expr, bool, error) { return expression, false, nil }
	var rewrite func(plan.Node) (plan.Node, error)
	rewrite = func(node plan.Node) (plan.Node, error) {
		project, ok := node.(*plan.ProjectNode)
		if !ok || pruned[project] == nil {
			return node, nil
		}
		copy := *pruned[project]
		child, err := rewriteQueryExpressions(copy.Child, identity, rewrite)
		if err != nil {
			return nil, err
		}
		copy.Child = child
		return &copy, nil
	}
	return rewriteQueryExpressions(root, identity, rewrite)
}

// Mutation consumers live outside their FROM/USING query root. Pass those real
// expressions into the same liveness pass, without inventing a project/provider
// or moving their execution frontier into the source.
func ruleMutationProducerDemand(b binding) (binding, error) {
	root := b.delete.using
	if b.update != nil {
		root = b.update.from
	}
	if root == nil {
		return b, nil
	}
	mapped, err := ruleQueryProducerDemand(root, func(visit func(expr.Expr)) {
		targets := b.arrayTargets
		if b.update != nil {
			for _, expression := range b.update.assignments {
				visit(expression)
			}
			visit(b.update.predicate)
			targets = b.update.arrayTargets
		} else {
			visit(b.predicate)
		}
		for _, target := range targets {
			for ; target != nil; target = target.Next {
				for _, subscript := range target.Node.Subscripts {
					visit(subscript.Lower)
					visit(subscript.Upper)
				}
			}
		}
		if b.returning != nil {
			for _, expression := range b.returning.expressions {
				visit(expression)
			}
		}
	}, len(b.relation.Columns))
	if err != nil {
		return binding{}, err
	}
	if mapped != root {
		if b.update != nil {
			copy := *b.update
			copy.from = mapped
			b.update = &copy
		} else {
			b.delete.using = mapped
		}
	}
	return b, nil
}
