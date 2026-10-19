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
	if hierarchyRelation(d) && mark.mode != 0 {
		e.mu.Unlock()
		return nil, sqlError("0A000", "row locking partition hierarchies is unsupported")
	}
	switch d.RelationKind {
	case catalog.ForeignRelation:
		source, err := e.captureForeignSourceLocked(scope, ref, d, invoker, outputUses, mark)
		e.mu.Unlock()
		return source, err
	case catalog.CatalogRelation:
		switch d.Address().ObjectID {
		case pgLocksOID:
			return e.capturePGLocksLocked(ctx, ref, d)
		case ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID, ids.ForeignTableClassID, pgUserMappingsOID:
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				e.mu.Unlock()
				return nil, err
			}
			source, err := e.captureForeignCatalog(ctx, auth, ref, d)
			e.mu.Unlock()
			return source, err
		case pgCursorsOID:
			cursors := scope.op.session.cursors
			e.mu.Unlock()
			return capturePGCursors(ctx, ref, d, cursors)
		case 12081, 12085:
			catalog, registry := scope.commentCatalog, e.catalog.Extensions
			e.mu.Unlock()
			return captureAvailableExtensions(ctx, registry, catalog, ref, d)
		case 12048:
			source, err := e.capturePGSequencesLocked(scope, ref, d)
			e.mu.Unlock()
			return source, err
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
		case tablePrivilegesOID:
			source, err := captureTablePrivileges(ctx, scope.commentCatalog, scope.deparseAuth, scope.op.session.databaseName, ref, d)
			e.mu.Unlock()
			return source, err
		case dataTypePrivilegesOID:
			source, err := captureDataTypePrivileges(ctx, scope.commentCatalog, scope.deparseAuth, scope.op.session.databaseName, ref, d)
			e.mu.Unlock()
			return source, err
		}
		if d.VirtualView {
			e.mu.Unlock()
			return nil, fmt.Errorf("stuntgres: unknown virtual catalog view")
		}
		if d.Address().ObjectID == 2396 {
			source, err := capturePGShDescription(ctx, scope.commentRoles, ref, d)
			e.mu.Unlock()
			return source, err
		}
		if d.Address().ObjectID == 2224 {
			base := e.catalog.state
			if scope.owner.sequenceCatalog != nil {
				base = scope.owner.sequenceCatalog
			}
			source, err := capturePGSequence(ctx, base, scope.owner.catalog, ref, d)
			e.mu.Unlock()
			return source, err
		}
		var catalog catalog.Read
		switch d.Address().ObjectID {
		case 2609, 3079, ids.ParameterClassID:
			catalog = scope.commentCatalog
		default:
			catalog = e.privateCatalogLocked(scope.owner.catalog)
		}
		var roles authorization.Read
		if d.Address().ObjectID == ids.ParameterClassID {
			roles = scope.commentRoles
		} else {
			roles = e.roles.read(e.catalog.owner(), catalog, scope.deparseAuth.RoleRecord)
		}
		if d.Address().ObjectID == 12002 {
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				e.mu.Unlock()
				return nil, err
			}
			auth.Catalog = catalog
			auth.Recapture(auth.Catalog)
			source, err := capturePGIndexes(ctx, catalog, auth, ref, d)
			e.mu.Unlock()
			return source, err
		}
		if d.Address().ObjectID == 12006 {
			source, err := e.captureTableViewSummary(ctx, catalog, roles, ref, d, viewPresentation{deparse: true, path: scope.deparsePath, output: scope.output, nonstandardStrings: scope.op.nonstandardStrings})
			e.mu.Unlock()
			return source, err
		}
		source, err := e.captureCatalogSourceLocked(ctx, catalog, roles, ref, d)
		e.mu.Unlock()
		return source, err
	case catalog.SequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.mu.Unlock()
		return source, err
	case catalog.ViewRelation:
		for _, active := range scope.activeViews {
			if active == d.Address() {
				e.mu.Unlock()
				return nil, sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.Name()))
			}
		}
		scope.activeViews = append(scope.activeViews, d.Address())
		defer func() { scope.activeViews = scope.activeViews[:len(scope.activeViews)-1] }()
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
	if mark.mode != 0 || viewHasStoredRowMarks(d, auth.Catalog) {
		return auth.checkViewRowMarks(d, mark, invoker)
	}
	return nil
}
