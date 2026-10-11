package engine

import "stuntgres/internal/sqltype"

type viewWriteOccurrence struct {
	definition *viewDefinition
	ordinal    int
}

// Only transplanted, demanded subqueries acquire an execution source slot.
// Frozen view layouts are bridged by capture, not by re-resolving SQL names.
func (w *viewWrite) graftQuery(b *binding, view CatalogRead, root LogicalNode, rewrite func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error) {
	project, ok := root.(*projectNode)
	if !ok {
		return rewriteQueryExpressions(root, rewrite)
	}
	var definition *viewDefinition
	var consumer *relationDescriptor
	for _, layer := range w.layers {
		if _, found := layer.relation.view.queries[project.syntax]; found {
			definition = layer.relation.view
			consumer = layer.relation
			break
		}
	}
	b.subqueryDemand = true
	if definition == nil {
		return rewriteQueryExpressions(root, rewrite)
	}
	return rewriteQueryExpressions(root, func(n BoundExpr) (BoundExpr, bool, error) {
		next, changed, err := rewrite(n)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			next = n
		}
		column, ok := next.(columnExpression)
		if !ok || column.origin == (ObjectRef{}) {
			return next, changed, nil
		}
		use, err := storedViewUseReference(view, consumer, column.origin)
		if err != nil {
			return nil, false, err
		}
		if column.origin != use {
			column.origin = use
			return column, true, nil
		}
		return next, changed, nil
	}, func(node LogicalNode) (LogicalNode, error) {
		source, ok := node.(*sourceNode)
		if !ok {
			return node, nil
		}
		if source.occurrence < 0 || source.occurrence >= len(definition.sources) {
			return nil, ErrCatalogReplaced
		}
		key := viewWriteOccurrence{definition, source.occurrence}
		slot, found := w.queryOccurrences[key]
		if !found {
			leaf := definition.sources[source.occurrence]
			relation, ok := view.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok {
				return nil, ErrCatalogReplaced
			}
			slot = len(b.querySources)
			if w.queryOccurrences == nil {
				w.queryOccurrences = make(map[viewWriteOccurrence]int)
			}
			w.queryOccurrences[key] = slot
			use, err := storedViewUseReference(view, consumer, leaf.ref)
			if err != nil {
				return nil, err
			}
			b.querySources = append(b.querySources, queryLeaf{ref: use, relation: relation, frozenAttributes: leaf.attributes, mark: leaf.mark})
			b.subqueryDemand = true
		}
		copy := *source
		copy.occurrence = slot
		copy.ref = b.querySources[slot].ref
		return &copy, nil
	})
}

func (e *Engine) attachViewWriteSourcesLocked(scope *StatementScope, b *binding) error {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	auth.catalog = e.privateCatalogLocked(scope.owner.catalog)
	original := len(b.viewWrite.logical.querySources)
	for i := original; i < len(b.querySources); i++ {
		source := &b.querySources[i]
		source.storage = e.privateStorageLocked(scope.owner.catalog, source.ref.Address)
		for key, ordinal := range b.viewWrite.queryOccurrences {
			if ordinal != i {
				continue
			}
			for _, layer := range b.viewWrite.layers {
				if layer.relation.view != key.definition {
					continue
				}
				current := auth.catalog.state.descriptors[layer.ref.Address].(*relationDescriptor)
				source.principal = current.owner
				if current.view.invoker {
					source.principal = auth.state.CurrentUser
				}
				break
			}
		}
		scope.querySources = append(scope.querySources, *source)
	}
	return nil
}

// Raw prefixes must be selected before frozen-layout bridges are installed.
// Detach bundle ownership while replacing occurrences, as stored view opening
// does; the returned head owns every input and each bridge's private scratch.
func freezeViewWriteSources(scope *StatementScope, source *querySource, leaves []queryLeaf) *querySource {
	sources := source.sources
	source.sources = nil
	for i, leaf := range leaves {
		child := source
		if sources != nil {
			child = sources[i]
		}
		child = frozenViewSource(scope, child, leaf.frozenAttributes, leaf.relation.attributes)
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
	return source
}

func frozenViewSource(scope *StatementScope, source *querySource, attributes, live []int32) *querySource {
	if attributes == nil {
		return source
	}
	unchanged := len(attributes) == len(live)
	if unchanged {
		for i, a := range attributes {
			if live[i] != a {
				unchanged = false
				break
			}
		}
	}
	if unchanged {
		return source
	}
	layout := make([]int, len(attributes))
	for i, a := range attributes {
		layout[i] = -1
		for j, b := range live {
			if a == b {
				layout[i] = j
				break
			}
		}
	}
	bridge := &querySource{ref: source.ref, input: source, layout: layout, scratch: make([]sqltype.Value, len(layout)), authority: scope.authority, locks: source.locks, occurrence: source.occurrence}
	bridge.borrowAuthority(scope)
	return bridge
}
