package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// prepareStoredQueryLocked consumes Engine.mu on all returns. Both ordinary
// views and refresh execute the frozen tree through current attribute bridges.
func (e *Engine) prepareStoredQueryLocked(scope *StatementScope, ref ids.ObjectRef, definition *viewDefinition, invoker authorization.PrincipalRef, mark plan.RowMark, locks *queryLockContext, outputUses []int) (plan.Node, *querySource, error) {
	ctx := scope.op.Context()
	if scope.authority == nil {
		scope.authority = &queryAuthority{scope: scope}
	}
	auth, err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark)
	if err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	e.mu.Unlock()
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, statementScope: scope, catalog: catalog, Input: scope.input, Output: scope.output}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	evaluation.quoteCaptures = scope.quoteCaptures
	evaluation.quoteOrigin = quoteOrigin{owner: ref, definition: definition.rule, revision: definition.quoteRevision}
	consumer, ok := catalog.Relation(ref.Address)
	if !ok || viewOf(consumer) != definition {
		return nil, nil, ids.ErrCatalogReplaced
	}
	evaluation.routineRecipe, evaluation.routineConsumer = true, consumer
	evaluation.routineChecks = definition.dependencies
	evaluation.routineAuthority = &auth
	root, reads, err := prepareStoredExecutionRoot(&evaluation, definition, outputUses)
	if err == nil {
		err = validateMarkedQueryJoin(&evaluation, root, catalog, nil, plan.RowMark{}, false, nil, reads)
	}
	if err != nil {
		return nil, nil, err
	}
	e.mu.Lock()
	if auth, err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	catalog = e.privateCatalogLocked(scope.owner.catalog)
	consumer = catalog.MustRelation(ref.Address)
	relationPrincipal := consumer.Owner
	if definition.invoker {
		relationPrincipal = invoker
	}
	leaves := make([]queryLeaf, len(definition.sources))
	for i, leaf := range definition.sources {
		relation := catalog.MustRelation(leaf.ref.Address)
		use, err := storedViewUseReference(catalog, consumer, leaf.ref)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		leaves[i] = queryLeaf{ref: use, relation: relation, only: leaf.only, principal: relationPrincipal, storage: e.privateStorageLocked(scope.owner.catalog, leaf.ref.Address), mark: viewLeafRowMark(definition, i, mark), analysisOnly: leaf.analysisOnly}
		leaves[i].uses, err = querySourceReadUses(reads[i], relation.Attributes)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
	}
	if scope.materializedOrZero().refresh && ref == scope.lockedRef {
		if err := e.initializeQueryLocksLocked(scope, root, leaves); err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || definition.query.VisibilitySnapshot
		locks = scope.queryLocks
	}
	source, err := e.captureQuerySourcesForInvokerLocked(scope, leaves, len(leaves) == 0, invoker, plan.RowMark{}, locks)
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
		live := leaves[i].relation.Attributes
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
	// Only this opaque stored tree borrows its defining recipe. User/grafted
	// replacements retain their existing per-node ancestry rules.
	source.routineRecipe = consumer
	return root, source, nil
}

// Legality and source opening consume the same execution-owned projection and
// scheduled producers. Frozen authorization reads and dependencies are unchanged.
func prepareStoredExecutionRoot(c *EvalContext, definition *viewDefinition, outputUses []int) (plan.Node, map[int][]int32, error) {
	demanded := definition.query.Root
	if project, ok := demanded.(*plan.ProjectNode); ok {
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
	if source, ok := root.(*plan.SourceNode); ok {
		if source.Occurrence < 0 || source.Occurrence >= len(definition.sources) {
			return nil, nil, fmt.Errorf("stuntgres: invalid stored source occurrence")
		}
		attributes := definition.sources[source.Occurrence].attributes
		if outputUses == nil {
			reads[source.Occurrence] = attributes
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
			reads[source.Occurrence] = items
		}
	}
	return root, reads, nil
}

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
		expressions[slot] = plan.LiteralExpr{expr.InfoOf(expression), sqltype.Null(expression.ResultUsage().Type)}
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
		left, right := queryInputs(node)
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
		walkBoundExpression(expression, func(node expr.Expr) {
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
				queryExpressions(node, visit)
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
			replacement[i] = plan.LiteralExpr{expr.InfoOf(expression), sqltype.Null(expression.ResultUsage().Type)}
		}
		if replacement == nil {
			return node, nil
		}
		next, err := rewriteQueryExpressions(child, identity, rewrite)
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
	return rewriteQueryExpressions(root, identity, rewrite)
}
