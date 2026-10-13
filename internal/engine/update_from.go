package engine

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *expressionBinder) mutationTargetNamespace() queryNamespace {
	columns := make([]queryColumn, len(b.relation.columns))
	for i, descriptor := range b.relation.columns {
		columns[i] = queryColumn{descriptor: descriptor, slot: i, leaf: queryTargetLeaf, physical: i}
	}
	name := b.targetName
	if b.targetAlias != "" {
		name = b.targetAlias
	}
	qualifier := queryQualifier{name: name, original: b.targetName, alias: b.targetAlias, ref: b.ref, columns: columns, schema: b.queryAuth.catalog.state.descriptors[b.relation.namespace].Name(), lateralForbidden: true}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: b.relation.columns}
}

// Expand recursive view bases before the first capture. Capture callers pass
// any already-required target refs, preserving a single statement frontier.
func (e *Engine) queryTableRefsLocked(catalog CatalogRead, leaves []queryLeaf, refs []ObjectRef, token SessionToken) ([]ObjectRef, error) {
	var visited map[ObjectRef]bool
	var collect func(ObjectRef, bool) error
	collect = func(ref ObjectRef, only bool) error {
		relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.owner != e.owner || ref.Epoch != catalog.Epoch() {
			return ErrCatalogReplaced
		}
		if relation.relationKind == viewRelation {
			if visited[ref] {
				return nil
			}
			if visited == nil {
				visited = make(map[ObjectRef]bool)
			}
			visited[ref] = true
			for _, child := range relation.view.sources {
				if child.analysisOnly {
					continue
				}
				use, err := storedViewUseReference(catalog, relation, child.ref)
				if err != nil {
					return err
				}
				if err := collect(use, child.only); err != nil {
					return err
				}
			}
		}
		if relation.partition != nil && !only {
			for _, address := range relation.partition.children {
				child, ok := catalog.state.descriptors[address].(*relationDescriptor)
				if !ok { return ErrCatalogReplaced }
				if err := collect(ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: address}, child.partition == nil); err != nil {
					return err
				}
			}
		}
		if relation.relationKind == tableRelation && !only {
			return inheritanceQueryClosure(catalog, relation, token, func(physical *relationDescriptor) error {
				child := ref
				child.Address = physical.address
				return collect(child, true)
			})
		}
		if relation.relationKind.stored() {
			for _, prior := range refs {
				if prior == ref {
					return nil
				}
			}
			refs = append(refs, ref)
		}
		return nil
	}
	for _, leaf := range leaves {
		if leaf.analysisOnly {
			continue
		}
		if err := collect(leaf.ref, leaf.only); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
