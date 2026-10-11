package engine

import (
	"fmt"
)

// openRelationSourceLocked consumes e.mu on every return. Definition relation
// principals affect ACL and row-security checks; expressions keep the invoker.
func (e *Engine) openRelationSourceLocked(scope *StatementScope, ref ObjectRef, invoker PrincipalRef, mark queryRowMark, locks *queryLockContext) (*querySource, error) {
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
	if err := inheritanceScanGuard(e.privateCatalogLocked(scope.owner.catalog), d); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if hierarchyRelation(d) && mark.mode != 0 {
		e.mu.Unlock()
		return nil, sqlError("0A000", "row locking partition hierarchies is unsupported")
	}
	switch d.relationKind {
	case catalogRelation:
		switch d.address.ObjectID {
		case 12004, 12005:
			input := scope.input
			e.mu.Unlock()
			return captureTimezoneSource(ctx, ref, d, input)
		case 12014:
			roles := scope.commentRoles
			e.mu.Unlock()
			return capturePGUser(ctx, roles, ref, d)
		case 12007:
			roles := scope.commentRoles
			e.mu.Unlock()
			return capturePGRoles(ctx, roles, ref, d)
		case 12008:
			roles := scope.commentRoles
			e.mu.Unlock()
			return capturePGGroup(ctx, roles, ref, d)
		}
		if d.virtualView {
			e.mu.Unlock()
			return nil, fmt.Errorf("stuntgres: unknown virtual catalog view")
		}
		if d.address.ObjectID == 2396 {
			source, err := capturePGShDescription(ctx, scope.commentRoles, ref, d)
			e.mu.Unlock()
			return source, err
		}
		if d.address.ObjectID == 2224 {
			base := e.state
			if scope.owner.sequenceCatalog != nil {
				base = scope.owner.sequenceCatalog
			}
			source, err := capturePGSequence(ctx, base, scope.owner.catalog, ref, d)
			e.mu.Unlock()
			return source, err
		}
		catalog := e.privateCatalogLocked(scope.owner.catalog)
		if d.address.ObjectID == 2609 {
			catalog = scope.commentCatalog
		}
		if d.address.ObjectID == 12002 {
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				e.mu.Unlock()
				return nil, err
			}
			auth.catalog = catalog
			source, err := capturePGIndexes(ctx, catalog, auth, ref, d)
			e.mu.Unlock()
			return source, err
		}
		if d.address.ObjectID == 12006 {
			source, err := e.captureTableViewSummary(ctx, catalog, ref, d, viewPresentation{deparse: true, path: scope.deparsePath, output: scope.output, nonstandardStrings: scope.op.nonstandardStrings})
			e.mu.Unlock()
			return source, err
		}
		source, err := e.captureCatalogSourceLocked(ctx, catalog, ref, d)
		e.mu.Unlock()
		return source, err
	case sequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.mu.Unlock()
		return source, err
	case viewRelation:
		for _, active := range scope.activeViews {
			if active == d.address {
				e.mu.Unlock()
				return nil, sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.name))
			}
		}
		scope.activeViews = append(scope.activeViews, d.address)
		defer func() { scope.activeViews = scope.activeViews[:len(scope.activeViews)-1] }()
		root, source, err := e.prepareStoredQueryLocked(scope, ref, d.view, invoker, mark, locks)
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
	case partitionedRelation:
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
	case tableRelation, materializedViewRelation:
		denied, securityErr := e.rowSecurityInputLocked(scope, d, invoker, policySelect, false)
		if securityErr != nil {
			e.mu.Unlock()
			return nil, securityErr
		}
		if d.relationKind == materializedViewRelation && !d.populated {
			e.mu.Unlock()
			return nil, materializedViewReadError(d)
		}
		view, err := scope.acquireDataViewLocked(e, ref)
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if denied {
			return captureDeniedQuerySource(view, ref)
		}
		return captureQuerySource(ctx, view, ref)
	case indexRelation:
		e.mu.Unlock()
		return nil, sqlError("42809", fmt.Sprintf(`cannot read from index "%s"`, d.name))
	default:
		e.mu.Unlock()
		return nil, fmt.Errorf("stuntgres: unsupported relation source")
	}
}

// Called before unlocked preflight and immediately before source capture. Each
// leaf checks current ACLs; an ordinary outer owner never replaces the invoker.
func (e *Engine) authorizeViewSourceLocked(scope *StatementScope, ref ObjectRef, definition *viewDefinition, invoker PrincipalRef, mark queryRowMark) error {
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
		if !ok || !storedViewReferenceValid(auth.catalog, d, leaf.ref) {
			return ErrCatalogReplaced
		}
		if leaf.analysisOnly {
			continue
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
		use, err := storedViewUseReference(auth.catalog, d, leaf.ref)
		if err != nil {
			return err
		}
		if err := auth.checkColumns(binding{ref: use, relation: source}, SelectPrivilege, indices); err != nil {
			return err
		}
	}
	if mark.mode != 0 || viewHasStoredRowMarks(d, auth.catalog) {
		return auth.checkViewRowMarks(d, mark, invoker)
	}
	return nil
}
