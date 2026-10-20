package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Mutation target columns retain independent physical write/read authority.
// Exposed source columns follow them; source-internal ON expressions stay local.
func (b *expressionBinder) mutationTargetNamespace() queryNamespace {
	columns := make([]queryColumn, len(b.target.relation.Columns))
	for i, descriptor := range b.target.relation.Columns {
		columns[i] = queryColumn{descriptor: descriptor, slot: i, leaf: queryTargetLeaf, physical: i}
	}
	name := b.target.name
	if b.target.alias != "" {
		name = b.target.alias
	}
	qualifier := queryQualifier{name: name, original: b.target.name, alias: b.target.alias, ref: b.target.ref, columns: columns, schema: b.query.auth.Catalog.Get(b.target.relation.Namespace()).Name(), lateralForbidden: true}
	return queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: b.target.relation.Columns}
}

// Namespace lookup and role privileges precede physical storage admission.
// Identity/metadata providers do not acquire another session's stored TEMP data.
func checkTemporaryStorageAccess(catalog catalog.Read, relation *catalog.Relation, token ids.SessionToken) error {
	if relation == nil {
		return ids.ErrCatalogReplaced
	}
	if !relation.RelationKind.Stored() {
		return nil
	}
	namespace, ok := catalog.Namespace(relation.Namespace())
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token {
		return sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	return nil
}

// Expand recursive view bases before the first capture. Capture callers pass
// any already-required target refs, preserving a single statement frontier.
func (e *Engine) queryTableRefsLocked(auth AuthorizationView, invoker authorization.PrincipalRef, leaves []queryLeaf, refs []ids.ObjectRef, token ids.SessionToken, inherited queryRowMark) ([]ids.ObjectRef, error) {
	view := auth.Catalog
	if auth.engine != e || view.Owner() != e.catalog.owner() || view.State() == nil {
		return nil, ids.ErrCatalogReplaced
	}
	// Authorize every executable view occurrence before admitting any physical
	// source or target. Only the recursion path detects cycles; shared views
	// retain each occurrence's row marks without a permission cache.
	var active []ids.ObjectRef
	var authorize func(ids.ObjectRef, authorization.PrincipalRef, queryRowMark) error
	authorize = func(ref ids.ObjectRef, sourceInvoker authorization.PrincipalRef, mark queryRowMark) error {
		relation, ok := view.Relation(ref.Address)
		if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if relation.RelationKind != catalog.ViewRelation {
			return nil
		}
		for _, prior := range active {
			if prior == ref {
				return nil
			}
		}
		mark = effectiveQueryRowMark(relation, mark)
		if err := authorizeViewSourceSnapshot(auth, ref, viewOf(relation), sourceInvoker, mark); err != nil {
			return err
		}
		active = append(active, ref)
		defer func() { active = active[:len(active)-1] }()
		for i, child := range viewOf(relation).sources {
			if child.analysisOnly {
				continue
			}
			use, err := storedViewUseReference(view, relation, child.ref)
			if err != nil {
				return err
			}
			// Nested invoker views retain this occurrence's original invoker;
			// each definer view selects its own owner in the canonical check.
			if err := authorize(use, sourceInvoker, viewLeafRowMark(viewOf(relation), i, mark)); err != nil {
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
		relation, ok := view.Relation(ref.Address)
		if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		if err := checkTemporaryStorageAccess(view, relation, token); err != nil {
			return nil, err
		}
	}
	var visited map[ids.ObjectRef]bool
	var collect func(ids.ObjectRef, bool) error
	collect = func(ref ids.ObjectRef, only bool) error {
		relation, ok := view.Relation(ref.Address)
		if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if relation.RelationKind == catalog.ViewRelation {
			if visited[ref] {
				return nil
			}
			if visited == nil {
				visited = make(map[ids.ObjectRef]bool)
			}
			visited[ref] = true
			for _, child := range viewOf(relation).sources {
				if child.analysisOnly {
					continue
				}
				use, err := storedViewUseReference(view, relation, child.ref)
				if err != nil {
					return err
				}
				if err := collect(use, child.only); err != nil {
					return err
				}
			}
		}
		if relation.Partition != nil && !only {
			for _, address := range relation.Partition.Children {
				child, ok := view.Relation(address)
				if !ok {
					return ids.ErrCatalogReplaced
				}
				if err := collect(ref.Domain().Ref(ref.Epoch, address), child.Partition == nil); err != nil {
					return err
				}
			}
		}
		if relation.RelationKind == catalog.TableRelation && !only {
			return inheritanceQueryClosure(view, relation, token, func(physical *catalog.Relation) error {
				child := ref
				child.Address = physical.Address()
				return collect(child, true)
			})
		}
		if relation.RelationKind.Stored() {
			if err := checkTemporaryStorageAccess(view, relation, token); err != nil {
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
