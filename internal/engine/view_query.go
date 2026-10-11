package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// prepareStoredQueryLocked consumes Engine.mu on all returns. Both ordinary
// views and refresh execute the frozen tree through current attribute bridges.
func (e *Engine) prepareStoredQueryLocked(scope *StatementScope, ref ObjectRef, definition *viewDefinition, invoker PrincipalRef, mark queryRowMark, locks *queryLockContext, outputUses []int) (LogicalNode, *querySource, error) {
	ctx := scope.op.Context()
	if scope.authority == nil {
		scope.authority = &queryAuthority{scope: scope}
	}
	if err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	e.mu.Unlock()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: scope, catalog: catalog, Input: scope.input, Output: scope.output}
	evaluation.quoteCaptures = scope.quoteCaptures
	evaluation.quoteOrigin = quoteOrigin{owner: ref, definition: definition.rule, revision: definition.quoteRevision}
	root, reads, err := prepareStoredExecutionRoot(&evaluation, definition, outputUses)
	if err == nil {
		err = validateMarkedQueryJoin(&evaluation, root, catalog, nil, queryRowMark{}, false, nil, reads)
	}
	if err != nil {
		return nil, nil, err
	}
	e.mu.Lock()
	if err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	catalog = e.privateCatalogLocked(scope.owner.catalog)
	consumer := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	relationPrincipal := consumer.owner
	if definition.invoker {
		relationPrincipal = invoker
	}
	leaves := make([]queryLeaf, len(definition.sources))
	for i, leaf := range definition.sources {
		relation := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		use, err := storedViewUseReference(catalog, consumer, leaf.ref)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		leaves[i] = queryLeaf{ref: use, relation: relation, only: leaf.only, principal: relationPrincipal, storage: e.privateStorageLocked(scope.owner.catalog, leaf.ref.Address), mark: viewLeafRowMark(definition, i, mark), analysisOnly: leaf.analysisOnly}
		leaves[i].uses, err = querySourceReadUses(reads[i], relation.attributes)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
	}
	if scope.materializedRefresh && ref == scope.lockedRef {
		if err := e.initializeQueryLocksLocked(scope, root, leaves); err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		scope.visibilityDemand = scope.visibilityDemand || definition.query.visibilitySnapshot
		locks = scope.queryLocks
	}
	source, err := e.captureQuerySourcesForInvokerLocked(scope, leaves, len(leaves) == 0, invoker, queryRowMark{}, locks)
	e.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if err = selectQuerySources(ctx, source); err != nil {
		source.close()
		return nil, nil, err
	}
	// Detach bundle ownership before replacing execution-local occurrences.
	sources := source.sources
	source.sources = nil
	for i, leaf := range definition.sources {
		child := source
		if sources != nil {
			child = sources[i]
		}
		live := leaves[i].relation.attributes
		unchanged := len(leaf.attributes) == len(live)
		if unchanged {
			for slot, attribute := range leaf.attributes {
				if attribute != live[slot] {
					unchanged = false
					break
				}
			}
		}
		if !unchanged {
			layout := make([]int, len(leaf.attributes))
			for slot, attribute := range leaf.attributes {
				layout[slot] = -1
				for current, id := range live {
					if attribute == id {
						layout[slot] = current
						break
					}
				}
			}
			child = &querySource{ref: leaves[i].ref, input: child, layout: layout, scratch: make([]sqltype.Value, len(layout)), authority: scope.authority, locks: child.locks, occurrence: child.occurrence}
			child.borrowAuthority(scope)
		}
		if leaf.ref != leaves[i].ref {
			child.storedRef = leaf.ref
		}
		if sources != nil {
			sources[i] = child
		} else {
			source = child
		}
	}
	if sources != nil {
		source = sources[0]
		source.sources = sources
	}
	source.borrowAuthority(scope)
	return root, source, nil
}

// Legality and source opening consume the same execution-owned projection and
// scheduled producers. Frozen authorization reads and dependencies are unchanged.
func prepareStoredExecutionRoot(c *EvalContext, definition *viewDefinition, outputUses []int) (LogicalNode, map[int][]int32, error) {
	demanded := definition.query.Root
	if project, ok := demanded.(*projectNode); ok {
		var err error
		demanded, err = storedProjectionDemand(project, outputUses)
		if err != nil {
			return nil, nil, err
		}
	} else {
		for _, slot := range outputUses {
			if slot < 0 || slot >= len(demanded.Schema()) {
				return nil, nil, fmt.Errorf("stuntgres: invalid stored projection use")
			}
		}
	}
	root, err := preflightQuery(c, demanded)
	if err != nil {
		return nil, nil, err
	}
	reads := queryExecutionSourceReads(root, nil)
	if source, ok := root.(*sourceNode); ok {
		if source.occurrence < 0 || source.occurrence >= len(definition.sources) {
			return nil, nil, fmt.Errorf("stuntgres: invalid stored source occurrence")
		}
		attributes := definition.sources[source.occurrence].attributes
		if outputUses == nil {
			reads[source.occurrence] = attributes
		} else {
			items := make([]int32, 0, len(outputUses))
			for _, slot := range outputUses {
				if slot >= len(attributes) {
					return nil, nil, fmt.Errorf("stuntgres: invalid stored source attribute")
				}
				found := false
				for _, attribute := range items {
					found = found || attribute == attributes[slot]
				}
				if !found {
					items = append(items, attributes[slot])
				}
			}
			reads[source.occurrence] = items
		}
	}
	return root, reads, nil
}

// Keep the frozen output layout while omitting only unreferenced payload
// demand. Clause/key stages and volatile outputs remain executable. nil uses
// means a full consumer (including materialized refresh); a nonnil empty list
// means only row presence is consumed. The stored definition is never changed.
func storedProjectionDemand(project *projectNode, uses []int) (*projectNode, error) {
	if uses == nil {
		return project, nil
	}
	for _, slot := range uses {
		if slot < 0 || slot >= len(project.expressions) {
			return nil, fmt.Errorf("stuntgres: invalid stored projection use")
		}
	}
	var expressions []BoundExpr
	for slot, expression := range project.expressions {
		if expression.Properties().Volatility == Volatile {
			continue
		}
		used := false
		for _, use := range uses {
			if use == slot {
				used = true
				break
			}
		}
		if used {
			continue
		}
		if expressions == nil {
			expressions = append([]BoundExpr(nil), project.expressions...)
		}
		expressions[slot] = literalExpression{boundExpressionInfo(expression), sqltype.Null(expression.ResultUsage().Type)}
	}
	if expressions == nil {
		return project, nil
	}
	child, err := storedScheduledPayloadDemand(project.child, expressions)
	if err != nil {
		return nil, err
	}
	copy := *project
	copy.expressions = expressions
	copy.child = child
	return &copy, nil
}

// Projection scheduling owns payload cache slots on the outer query's unary
// path. Follow those existing owners, not source/derived-query frames, and
// retain the transitive slot reads of selected payloads and internal clauses.
func storedScheduledPayloadDemand(root LogicalNode, retained []BoundExpr) (LogicalNode, error) {
	input := func(node LogicalNode) LogicalNode {
		switch node.(type) {
		case *projectNode, *derivedNode, *joinNode, *setNode:
			return nil
		}
		left, right := queryInputs(node)
		if right != nil {
			return nil
		}
		return left
	}
	var producers map[int]BoundExpr
	var owners map[LogicalNode]int
	for node := root; node != nil; node = input(node) {
		var expressions []BoundExpr
		var base int
		switch n := node.(type) {
		case *sortNode:
			expressions, base = n.outputs, n.cacheWidth-len(n.outputs)
		case *evaluationNode:
			expressions, base = n.expressions, n.base
		}
		if len(expressions) == 0 {
			continue
		}
		if producers == nil {
			producers = make(map[int]BoundExpr)
			owners = make(map[LogicalNode]int)
		}
		owners[node] = base
		for i, expression := range expressions {
			producers[base+i] = expression
		}
	}
	if len(producers) == 0 {
		return root, nil
	}
	live := make(map[int]bool)
	var demand func(BoundExpr)
	demand = func(expression BoundExpr) {
		walkBoundExpression(expression, func(node BoundExpr) {
			cache, ok := node.(cachedOrderExpression)
			if !ok || live[cache.slot] {
				return
			}
			live[cache.slot] = true
			if producer := producers[cache.slot]; producer != nil {
				demand(producer)
			}
		})
	}
	for _, expression := range retained {
		demand(expression)
	}
	for slot, producer := range producers {
		if producer.Properties().Volatility == Volatile {
			live[slot] = true
			demand(producer)
		}
	}
	for node := root; node != nil; node = input(node) {
		switch n := node.(type) {
		case *sortNode:
			for _, key := range n.keys {
				demand(key.expression)
			}
		case *evaluationNode:
			// Its payload reads are demanded only through live owner slots.
		case *projectNode, *derivedNode, *joinNode, *setNode:
			// These are separate projection/cache frames.
		default:
			queryExpressions(node, demand)
		}
	}
	identity := func(expression BoundExpr) (BoundExpr, bool, error) {
		return expression, false, nil
	}
	var rewrite func(LogicalNode) (LogicalNode, error)
	rewrite = func(node LogicalNode) (LogicalNode, error) {
		base, owned := owners[node]
		if !owned {
			return node, nil
		}
		var expressions []BoundExpr
		var child LogicalNode
		switch n := node.(type) {
		case *sortNode:
			expressions, child = n.outputs, n.child
		case *evaluationNode:
			expressions, child = n.expressions, n.child
		}
		var replacement []BoundExpr
		for i, expression := range expressions {
			if live[base+i] {
				continue
			}
			if replacement == nil {
				replacement = append([]BoundExpr(nil), expressions...)
			}
			replacement[i] = literalExpression{boundExpressionInfo(expression), sqltype.Null(expression.ResultUsage().Type)}
		}
		if replacement == nil {
			return node, nil
		}
		next, err := rewriteQueryExpressions(child, identity, rewrite)
		if err != nil {
			return nil, err
		}
		switch n := node.(type) {
		case *sortNode:
			copy := *n
			copy.child, copy.outputs = next, replacement
			return &copy, nil
		case *evaluationNode:
			copy := *n
			copy.child, copy.expressions = next, replacement
			return &copy, nil
		}
		return node, nil
	}
	return rewriteQueryExpressions(root, identity, rewrite)
}
