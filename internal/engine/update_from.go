package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

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

// Namespace lookup and role privileges precede physical storage admission.
// Identity/metadata providers do not acquire another session's stored TEMP data.
func checkTemporaryStorageAccess(catalog CatalogRead, relation *relationDescriptor, token ids.SessionToken) error {
	if relation == nil {
		return ErrCatalogReplaced
	}
	if !relation.relationKind.stored() {
		return nil
	}
	namespace, ok := catalog.state.descriptors[relation.namespace].(*namespaceDescriptor)
	if !ok {
		return ErrCatalogReplaced
	}
	if namespace.temporary != (ids.SessionToken{}) && namespace.temporary != token {
		return sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	return nil
}

// Expand recursive view bases before the first capture. Capture callers pass
// any already-required target refs, preserving a single statement frontier.
func (e *Engine) queryTableRefsLocked(auth AuthorizationView, invoker authorization.PrincipalRef, leaves []queryLeaf, refs []ids.ObjectRef, token ids.SessionToken, inherited queryRowMark) ([]ids.ObjectRef, error) {
	catalog := auth.catalog
	if auth.engine != e || catalog.owner != e || catalog.state == nil {
		return nil, ErrCatalogReplaced
	}
	// Authorize every executable view occurrence before admitting any physical
	// source or target. Only the recursion path detects cycles; shared views
	// retain each occurrence's row marks without a permission cache.
	var active []ids.ObjectRef
	var authorize func(ids.ObjectRef, authorization.PrincipalRef, queryRowMark) error
	authorize = func(ref ids.ObjectRef, sourceInvoker authorization.PrincipalRef, mark queryRowMark) error {
		relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.Domain() != e.domain || ref.Epoch != catalog.Epoch() {
			return ErrCatalogReplaced
		}
		if relation.relationKind != viewRelation {
			return nil
		}
		for _, prior := range active {
			if prior == ref {
				return nil
			}
		}
		mark = effectiveQueryRowMark(relation, mark)
		if err := authorizeViewSourceSnapshot(auth, ref, relation.view, sourceInvoker, mark); err != nil {
			return err
		}
		active = append(active, ref)
		defer func() { active = active[:len(active)-1] }()
		for i, child := range relation.view.sources {
			if child.analysisOnly {
				continue
			}
			use, err := storedViewUseReference(catalog, relation, child.ref)
			if err != nil {
				return err
			}
			// Nested invoker views retain this occurrence's original invoker;
			// each definer view selects its own owner in the canonical check.
			if err := authorize(use, sourceInvoker, viewLeafRowMark(relation.view, i, mark)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, leaf := range leaves {
		if !leaf.analysisOnly {
			principal := leaf.principal
			if principal == (authorization.PrincipalRef{}) {
				principal = invoker
			}
			if err := authorize(leaf.ref, principal, mergeQueryRowMark(inherited, leaf.mark)); err != nil {
				return nil, err
			}
		}
	}
	for _, ref := range refs {
		relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.Domain() != e.domain || ref.Epoch != catalog.Epoch() {
			return nil, ErrCatalogReplaced
		}
		if err := checkTemporaryStorageAccess(catalog, relation, token); err != nil {
			return nil, err
		}
	}
	var visited map[ids.ObjectRef]bool
	var collect func(ids.ObjectRef, bool) error
	collect = func(ref ids.ObjectRef, only bool) error {
		relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.Domain() != e.domain || ref.Epoch != catalog.Epoch() {
			return ErrCatalogReplaced
		}
		if relation.relationKind == viewRelation {
			if visited[ref] {
				return nil
			}
			if visited == nil {
				visited = make(map[ids.ObjectRef]bool)
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
				if !ok {
					return ErrCatalogReplaced
				}
				if err := collect(ref.Domain().Ref(ref.Epoch, address), child.partition == nil); err != nil {
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
			if err := checkTemporaryStorageAccess(catalog, relation, token); err != nil {
				return err
			}
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
