package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// Expand recursive view bases before the first capture. Capture callers pass
// any already-required target refs, preserving a single statement frontier.
func (e *Engine) queryTableRefsLocked(auth AuthorizationView, invoker authorization.PrincipalRef, leaves []exec.Leaf, refs []ids.ObjectRef, token ids.SessionToken, inherited plan.RowMark) ([]ids.ObjectRef, error) {
	view := auth.Catalog
	if auth.engine != e || view.Owner() != e.catalog.owner() || view.State() == nil {
		return nil, ids.ErrCatalogReplaced
	}
	// Authorize every executable view occurrence before admitting any physical
	// source or target. Only the recursion path detects cycles; shared views
	// retain each occurrence's row marks without a permission cache.
	var active []ids.ObjectRef
	var authorize func(ids.ObjectRef, authorization.PrincipalRef, plan.RowMark) error
	authorize = func(ref ids.ObjectRef, sourceInvoker authorization.PrincipalRef, mark plan.RowMark) error {
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
		mark = exec.EffectiveQueryRowMark(relation, mark)
		if err := authorizeViewSourceSnapshot(auth, ref, viewOf(relation), sourceInvoker, mark); err != nil {
			return err
		}
		active = append(active, ref)
		defer func() { active = active[:len(active)-1] }()
		for i, child := range viewOf(relation).Sources() {
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
		if !leaf.AnalysisOnly {
			principal := leaf.Principal
			if principal == (authorization.PrincipalRef{}) {
				principal = invoker
			}
			if err := authorize(leaf.Ref, principal, exec.MergeQueryRowMark(inherited, leaf.Mark)); err != nil {
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
			for _, child := range viewOf(relation).Sources() {
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
		if leaf.AnalysisOnly {
			continue
		}
		if err := collect(leaf.Ref, leaf.Only); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
