package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// openRelationSourceLocked consumes e.mu on every return. Definition relation
// principals affect ACL checks only; the operation remains the original invoker.
func (e *Engine) openRelationSourceLocked(scope *StatementScope, ref ObjectRef, invoker PrincipalRef, mode relationLockMode, locks *queryLockContext) (*querySource, error) {
	ctx := scope.op.Context()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	d, exists := e.privateCatalogLocked(scope.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || !exists {
		e.mu.Unlock()
		return nil, ErrCatalogReplaced
	}
	switch d.relationKind {
	case catalogRelation:
		source, err := e.captureCatalogSourceLocked(ctx, e.privateCatalogLocked(scope.owner.catalog), ref, d)
		e.mu.Unlock()
		return source, err
	case sequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.mu.Unlock()
		return source, err
	case viewRelation:
		definition := d.view
		// An unmarked sibling still needs the enclosing occurrence/lineage
		// manifest for replacement recheck. Retain that identity while mode
		// stays zero; participation never grants an otherwise absent mark.
		if err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mode); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		e.mu.Unlock()
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
		root, err := preflightQuery(&evaluation, definition.query.Root)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		if err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mode); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		catalog := e.privateCatalogLocked(scope.owner.catalog)
		leaves := make([]queryLeaf, len(definition.sources))
		for i, leaf := range definition.sources {
			relation := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			leaves[i] = queryLeaf{ref: leaf.ref, relation: relation, storage: e.privateStorageLocked(scope.owner.catalog, leaf.ref.Address)}
		}
		source, err := e.captureQuerySourcesForInvokerLocked(scope, leaves, len(leaves) == 0, invoker, viewRowMode(definition, mode), locks)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err = selectQuerySources(ctx, source); err != nil {
			return nil, err
		}
		// Detach bundle ownership while replacing individual occurrences with their
		// execution-local frozen-slot bridges. Repeated physical leaves never share scratch.
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
				child = &querySource{ref: leaf.ref, input: child, layout: layout, scratch: make([]sqltype.Value, len(layout)), scope: scope, locks: child.locks, occurrence: child.occurrence}
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
		source.scope = scope
		bridge := &querySource{ref: ref, scope: scope, locks: locks, nestedRoot: root, nestedInputs: source, materialized: true, flatWidth: len(root.Schema())}
		if locks != nil {
			occurrences := queryTreeOccurrences(root, source, nil)
			bridge.lineage = &queryLockLineage{root: root, inputs: source, stage: &querySource{materialized: true, occurrences: occurrences}}
		}
		return bridge, nil
	case tableRelation:
		view, err := scope.acquireDataViewLocked(e, ref)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return captureQuerySource(ctx, view, ref)
	default:
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: unsupported relation source")
	}
}

// Called before unlocked preflight and immediately before source capture. Each
// leaf checks current ACLs; an ordinary outer owner never replaces the invoker.
func (e *Engine) authorizeViewSourceLocked(scope *StatementScope, ref ObjectRef, definition *viewDefinition, invoker PrincipalRef, mode relationLockMode) error {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	auth.catalog = e.privateCatalogLocked(scope.owner.catalog)
	d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.owner != e.owner || ref.Epoch != auth.catalog.Epoch() || d.view != definition {
		return ErrCatalogReplaced
	}
	principal := d.owner
	if definition.invoker {
		principal = invoker
	}
	auth.state.CurrentUser = principal
	for _, leaf := range definition.sources {
		source, ok := auth.catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok || leaf.ref.owner != e.owner || leaf.ref.Epoch != auth.catalog.Epoch() {
			return ErrCatalogReplaced
		}
		indices := make([]int, 0, len(leaf.reads))
		for _, attribute := range leaf.reads {
			found := false
			for slot, live := range source.attributes {
				if attribute == live {
					indices = append(indices, slot)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing view source attribute")
			}
		}
		if err := auth.checkColumns(binding{ref: leaf.ref, relation: source}, SelectPrivilege, indices); err != nil {
			return err
		}
	}
	if mode != 0 || viewHasStoredRowMarks(d, auth.catalog) {
		return auth.checkViewRowMarks(d, mode, invoker)
	}
	return nil
}
