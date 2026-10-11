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
	for _, layer := range w.layers {
		if _, found := layer.relation.view.queries[project.syntax]; found {
			definition = layer.relation.view
			break
		}
	}
	b.subqueryDemand = true
	if definition == nil {
		return rewriteQueryExpressions(root, rewrite)
	}
	return rewriteQueryExpressions(root, rewrite, func(node LogicalNode) (LogicalNode, error) {
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
			b.querySources = append(b.querySources, queryLeaf{ref: leaf.ref, relation: relation, frozenAttributes: leaf.attributes, mark: leaf.mark})
			b.subqueryDemand = true
		}
		copy := *source
		copy.occurrence = slot
		return &copy, nil
	})
}

func (e *Engine) attachViewWriteSourcesLocked(scope *StatementScope, b *binding) error {
	original := len(b.viewWrite.logical.querySources)
	for i := original; i < len(b.querySources); i++ {
		source := &b.querySources[i]
		source.storage = e.privateStorageLocked(scope.owner.catalog, source.ref.Address)
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
