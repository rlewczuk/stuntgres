package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// openRelationSourceLocked consumes e.mu on every return. Definition relation
// principals affect ACL and row-security checks; expressions keep the invoker.
func (e *Engine) openRelationSourceLocked(scope *StatementScope, ref ids.ObjectRef, invoker authorization.PrincipalRef, mark queryRowMark, locks *queryLockContext, outputUses []int) (*querySource, error) {
	ctx := scope.op.Context()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	d, exists := e.privateCatalogLocked(scope.owner.catalog).Relation(ref.Address)
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || !exists {
		e.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	if hierarchyRelation(d) && mark.Mode != 0 {
		e.mu.Unlock()
		return nil, sqlError("0A000", "row locking partition hierarchies is unsupported")
	}
	switch d.RelationKind {
	case catalog.ForeignRelation:
		source, err := e.captureForeignSourceLocked(scope, ref, d, invoker, outputUses, mark)
		e.mu.Unlock()
		return source, err
	case catalog.CatalogRelation:
		return e.openCatalogSourceLocked(scope, ref, d)
	case catalog.SequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.mu.Unlock()
		return source, err
	case catalog.ViewRelation:
		for _, active := range scope.viewCreationOrZero().active {
			if active == d.Address() {
				e.mu.Unlock()
				return nil, sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.Name()))
			}
		}
		views := scope.viewCreation()
		views.active = append(views.active, d.Address())
		defer func() { views.active = views.active[:len(views.active)-1] }()
		root, source, err := e.prepareStoredQueryLocked(scope, ref, viewOf(d), invoker, mark, locks, outputUses)
		if err != nil {
			return nil, err
		}
		bridge := &querySource{ref: ref, authority: scope.authority, locks: locks, nestedRoot: root, nestedInputs: source, materialized: true, flatWidth: len(root.Schema()), inherited: mark}
		bridge.borrowAuthority(scope)
		if locks != nil {
			occurrences := queryTreeOccurrences(root, source, nil)
			bridge.lineage = &queryLockLineage{root: root, inputs: source, stage: &querySource{materialized: true, occurrences: occurrences}}
		}
		return bridge, nil
	case catalog.PartitionedRelation:
		var leaves [1]queryLeaf
		leaves[0] = queryLeaf{ref: ref, relation: d}
		source, err := e.captureQuerySourcesForInvokerLocked(scope, leaves[:], false, invoker, mark, locks)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err = selectQuerySources(ctx, source); err != nil {
			source.close()
			return nil, err
		}
		return source, nil
	case catalog.IndexRelation:
		e.mu.Unlock()
		return nil, sqlError("42809", fmt.Sprintf(`cannot read from index "%s"`, d.Name()))
	default:
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: unsupported relation source")
	}
}

// Called before unlocked preflight and immediately before source capture. Each
// leaf checks current ACLs; an ordinary outer owner never replaces the invoker.
func (e *Engine) authorizeViewSourceLocked(scope *StatementScope, ref ids.ObjectRef, definition *viewDefinition, invoker authorization.PrincipalRef, mark queryRowMark) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return AuthorizationView{}, err
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.Recapture(auth.Catalog)
	return auth, authorizeViewSourceSnapshot(auth, ref, definition, invoker, mark)
}

// The source collector and execution fences use the same captured owner/invoker
// checks; neither substitutes a live role/catalog sample during recursion.
func authorizeViewSourceSnapshot(auth AuthorizationView, ref ids.ObjectRef, definition *viewDefinition, invoker authorization.PrincipalRef, mark queryRowMark) error {
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != auth.Owner.Domain || ref.Epoch != auth.Catalog.Epoch() || viewOf(d) != definition {
		return ids.ErrCatalogReplaced
	}
	principal := d.Owner
	if definition.invoker {
		principal = invoker
	}
	auth.State.CurrentUser = principal
	for _, leaf := range definition.sources {
		source, ok := auth.Catalog.Relation(leaf.ref.Address)
		if !ok || !storedViewReferenceValid(auth.Catalog, d, leaf.ref) {
			return ids.ErrCatalogReplaced
		}
		if leaf.analysisOnly {
			continue
		}
		indices := make([]int, 0, len(leaf.reads))
		for _, attribute := range leaf.reads {
			found := false
			for slot, live := range source.Attributes {
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
		use, err := storedViewUseReference(auth.Catalog, d, leaf.ref)
		if err != nil {
			return err
		}
		if err := auth.checkColumns(binding{ref: use, relation: source}, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
	}
	if mark.Mode != 0 || viewHasStoredRowMarks(d, auth.Catalog) {
		return auth.checkViewRowMarks(d, mark, invoker)
	}
	return nil
}
