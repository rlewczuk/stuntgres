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
	mapOccurrence := func(occurrence int) (int, error) {
		if occurrence < 0 || occurrence >= len(definition.sources) {
			return 0, ErrCatalogReplaced
		}
		key := viewWriteOccurrence{definition, occurrence}
		slot, found := w.queryOccurrences[key]
		if !found {
			leaf := definition.sources[occurrence]
			relation, ok := view.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok {
				return 0, ErrCatalogReplaced
			}
			slot = len(b.querySources)
			if w.queryOccurrences == nil {
				w.queryOccurrences = make(map[viewWriteOccurrence]int)
			}
			w.queryOccurrences[key] = slot
			use, err := storedViewUseReference(view, consumer, leaf.ref)
			if err != nil {
				return 0, err
			}
			b.querySources = append(b.querySources, queryLeaf{ref: use, relation: relation, frozenAttributes: leaf.attributes, mark: leaf.mark})
			b.subqueryDemand = true
		}
		return slot, nil
	}
	// Structural derived inputs belong to this transplant. Scalar child queries
	// are lowered by rewrite's explicit query callback and must not map twice.
	frames := make(map[uint32]bool)
	var ownFrames func(LogicalNode)
	ownFrames = func(node LogicalNode) {
		if node == nil {
			return
		}
		if project, ok := node.(*projectNode); ok {
			frames[project.frame] = true
		}
		left, right := queryInputs(node)
		ownFrames(left)
		ownFrames(right)
	}
	ownFrames(root)
	translate := func(n BoundExpr) (BoundExpr, bool, error) {
		column, ok := n.(columnExpression)
		if !ok || column.origin == (ObjectRef{}) || !frames[column.frame] {
			return n, false, nil
		}
		changed := false
		if column.queryOccurrence != 0 {
			slot, err := mapOccurrence(column.queryOccurrence - 1)
			if err != nil {
				return nil, false, err
			}
			column.queryOccurrence = slot + 1
			changed = true
		}
		use, err := storedViewUseReference(view, consumer, column.origin)
		if err != nil {
			return nil, false, err
		}
		changed = changed || column.origin != use
		column.origin = use
		return column, changed, nil
	}
	return rewriteQueryExpressions(root, func(n BoundExpr) (BoundExpr, bool, error) {
		next, translated, err := mapBoundExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, translate)
		if err != nil {
			return nil, false, err
		}
		lowered, changed, err := rewrite(next)
		return lowered, translated || changed, err
	}, func(node LogicalNode) (LogicalNode, error) {
		source, ok := node.(*sourceNode)
		if !ok {
			return node, nil
		}
		slot, err := mapOccurrence(source.occurrence)
		if err != nil {
			return nil, err
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
	auth.recaptureCatalog(auth.catalog)
	original := len(b.viewWrite.logical.querySources)
	for i := original; i < len(b.querySources); i++ {
		rewritten := false
		for _, ordinal := range b.viewWrite.queryOccurrences {
			if ordinal == i {
				rewritten = true
				break
			}
		}
		if !rewritten {
			continue
		}
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
		if len(scope.mutations) == 0 {
			scope.querySources = append(scope.querySources, *source)
		}
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
