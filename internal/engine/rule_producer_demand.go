package engine

import "slices"

// Demand is derived from typed NEW references before substitution. The ordinary
// stored projection planner also retains volatile outputs and clause caches.
func ruleProducerOutputUses(original binding, rule ruleDefinition, action binding) []int {
	uses := []int{}
	inspect := func(expr BoundExpr) {
		walkBoundExpression(expr, func(node BoundExpr) {
			column, ok := node.(columnExpression)
			if !ok || column.scope != rulePseudoScope || column.frame != 2 {
				return
			}
			target := slices.Index(original.relation.attributes, column.origin.Address.SubID)
			for output, slot := range original.targets {
				if slot == target && !slices.Contains(uses, output) {
					uses = append(uses, output)
				}
			}
		})
	}
	query := func(root LogicalNode) { visitCTEQuery(root, func(LogicalNode) {}, inspect, true) }
	expression := func(expr BoundExpr) {
		if expr != nil {
			query(&projectNode{expressions: []BoundExpr{expr}})
		}
	}
	targets := func(items []*arrayAssignmentTarget) {
		for _, target := range items {
			for ; target != nil; target = target.next {
				for _, sub := range target.node.subscripts {
					expression(sub.lower)
					expression(sub.upper)
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
	query(action.deleteUsing)
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
	if action.conflict != nil {
		expression(action.conflict.predicate)
		for _, key := range action.conflict.keys {
			expression(key.expression)
		}
		if action.conflict.update != nil {
			expression(action.conflict.update.predicate)
			for _, expr := range action.conflict.update.assignments {
				expression(expr)
			}
			targets(action.conflict.update.arrayTargets)
		}
	}
	return uses
}

// Apply ordinary projection liveness at each derived producer boundary before
// rule-program static preparation. Slots are interpreted in their retained
// lexical input frame, including the offset contributed by preceding join arms.
// Analysis-only expressions are not executable demand.
func ruleQueryProducerDemand(root LogicalNode, external func(func(BoundExpr)), targetPrefix int) (LogicalNode, error) {
	type producer struct {
		frame   uint32
		offset  int
		demand  []bool
		project *projectNode
		uses    []int
		active  bool
		dirty   bool
	}
	var producers []*producer
	bySource := make(map[LogicalNode]*producer)
	var collect func(LogicalNode, int, uint32, uint32)
	collect = func(node LogicalNode, offset int, exposedFrame, inputFrame uint32) {
		if node == nil {
			return
		}
		if derived, ok := node.(*derivedNode); ok {
			if bySource[derived] != nil || derived.query == nil {
				return
			}
			project, ok := derived.query.Root.(*projectNode)
			if !ok {
				return
			}
			frame := derived.scanFrame
			if frame == 0 {
				// Synthetic producers inherit their consumer's current frame;
				// zero is not a separate lexical namespace.
				frame = inputFrame
			}
			item := &producer{frame: frame, offset: offset, demand: derived.demand, project: project, uses: []int{}}
			bySource[derived] = item
			producers = append(producers, item)
			// openDerivedNode creates a fresh inner query state. Its project
			// supplies an explicit frame or uses the ordinary default frame.
			collect(project, 0, 0, 1)
			return
		}
		if project, projected := node.(*projectNode); projected {
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
			if project.frame != 0 {
				inputFrame = project.frame
			}
		}
		left, right := queryInputs(node)
		if join, joined := node.(*joinNode); joined {
			frame := inputFrame
			if join.frame != 0 {
				frame = join.frame
			}
			collect(left, offset, frame, frame)
			collect(right, offset+len(left.Schema()), frame, frame)
		} else {
			collect(left, offset, 0, inputFrame)
			collect(right, offset, 0, inputFrame)
		}
		queryExpressions(node, func(expression BoundExpr) {
			walkDemandedBoundExpression(expression, func(node BoundExpr) {
				if subquery, ok := node.(subqueryExpression); ok && !subquery.cteDefinition && subquery.queryPlan != nil {
					collect(subquery.queryPlan.Root, 0, 0, 1)
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
	var observe func(LogicalNode)
	var inspect func(BoundExpr, int)
	inspect = func(expression BoundExpr, prefix int) {
		walkDemandedBoundExpression(expression, func(node BoundExpr) {
			if column, ok := node.(columnExpression); ok {
				for _, item := range producers {
					slot := column.slot - prefix - item.offset
					if column.frame != item.frame || slot < 0 || slot >= len(item.project.expressions) {
						continue
					}
					activate(item)
					if !slices.Contains(item.uses, slot) {
						item.uses = append(item.uses, slot)
						enqueue(item)
					}
				}
			}
			if subquery, ok := node.(subqueryExpression); ok && !subquery.cteDefinition && subquery.queryPlan != nil {
				observe(subquery.queryPlan.Root)
			}
		})
	}
	observe = func(node LogicalNode) {
		if node == nil || failure != nil {
			return
		}
		if item := bySource[node]; item != nil {
			activate(item)
			return
		}
		switch n := node.(type) {
		case *derivedNode:
			// Modifying scans have no SELECT graph and retain their spool key.
			return
		case *projectNode:
			failure = storedProjectionDemandExpressions(n, nil, func(expression BoundExpr) {
				inspect(expression, 0)
			}, observe)
		default:
			queryExpressions(node, func(expression BoundExpr) { inspect(expression, 0) })
			left, right := queryInputs(node)
			observe(left)
			observe(right)
		}
	}
	observe(root)
	if external != nil {
		external(func(expression BoundExpr) { inspect(expression, targetPrefix) })
	}
	// Original immutable recipes remain intact until liveness converges. A
	// retained payload can read a supplier in an outer lexical frame; each new
	// output use enqueues that supplier, never its executed rows or invocation.
	for next := 0; next < len(queue) && failure == nil; next++ {
		item := queue[next]
		item.dirty = false
		failure = storedProjectionDemandExpressions(item.project, item.uses, func(expression BoundExpr) {
			inspect(expression, 0)
		}, observe)
	}
	if failure != nil {
		return nil, failure
	}
	uses := make(map[*projectNode][]int)
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
	pruned := make(map[*projectNode]*projectNode)
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
	identity := func(expression BoundExpr) (BoundExpr, bool, error) { return expression, false, nil }
	var rewrite func(LogicalNode) (LogicalNode, error)
	rewrite = func(node LogicalNode) (LogicalNode, error) {
		project, ok := node.(*projectNode)
		if !ok || pruned[project] == nil {
			return node, nil
		}
		copy := *pruned[project]
		child, err := rewriteQueryExpressions(copy.child, identity, rewrite)
		if err != nil {
			return nil, err
		}
		copy.child = child
		return &copy, nil
	}
	return rewriteQueryExpressions(root, identity, rewrite)
}

// Mutation consumers live outside their FROM/USING query root. Pass those real
// expressions into the same liveness pass, without inventing a project/provider
// or moving their execution frontier into the source.
func ruleMutationProducerDemand(b binding) (binding, error) {
	root := b.deleteUsing
	if b.update != nil {
		root = b.update.from
	}
	if root == nil {
		return b, nil
	}
	mapped, err := ruleQueryProducerDemand(root, func(visit func(BoundExpr)) {
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
			for ; target != nil; target = target.next {
				for _, subscript := range target.node.subscripts {
					visit(subscript.lower)
					visit(subscript.upper)
				}
			}
		}
		if b.returning != nil {
			for _, expression := range b.returning.expressions {
				visit(expression)
			}
		}
	}, len(b.relation.columns))
	if err != nil {
		return binding{}, err
	}
	if mapped != root {
		if b.update != nil {
			copy := *b.update
			copy.from = mapped
			b.update = &copy
		} else {
			b.deleteUsing = mapped
		}
	}
	return b, nil
}
