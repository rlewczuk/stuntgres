package engine

import "stuntgres/internal/sqltype"

// prepareStoredQueryLocked consumes Engine.mu on all returns. Both ordinary
// views and refresh execute the frozen tree through current attribute bridges.
func (e *Engine) prepareStoredQueryLocked(scope *StatementScope, ref ObjectRef, definition *viewDefinition, invoker PrincipalRef, mark queryRowMark, locks *queryLockContext) (LogicalNode, *querySource, error) {
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
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
	root, err := preflightQuery(&evaluation, definition.query.Root)
	if err == nil {
		err = validateQueryJoinExecution(&evaluation, root, catalog, 0)
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
	leaves := make([]queryLeaf, len(definition.sources))
	for i, leaf := range definition.sources {
		relation := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		leaves[i] = queryLeaf{ref: leaf.ref, relation: relation, storage: e.privateStorageLocked(scope.owner.catalog, leaf.ref.Address), mark: viewLeafRowMark(definition, i, mark)}
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
			child = &querySource{ref: leaf.ref, input: child, layout: layout, scratch: make([]sqltype.Value, len(layout)), authority: scope.authority, locks: child.locks, occurrence: child.occurrence}
			child.borrowAuthority(scope)
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
