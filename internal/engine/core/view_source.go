package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// openRelationSourceLocked consumes e.mu on every return. Definition relation
// principals affect ACL and row-security checks; expressions keep the invoker.
// keep is the catalog producer row test (see catalogSourceKeep), or zero.
func (e *Engine) openRelationSourceLocked(scope *StatementScope, ref ids.ObjectRef, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int, keep pgcatalog.Keep) (*exec.Source, error) {
	ctx := scope.Op.Context()
	if err := ctx.Err(); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	d, exists := e.PrivateCatalogLocked(scope.PendingCatalog()).Relation(ref.Address)
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || !exists {
		e.Mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	if catalog.HierarchyRelation(d) && mark.Mode != 0 {
		e.Mu.Unlock()
		return nil, SQLError("0A000", "row locking partition hierarchies is unsupported")
	}
	switch d.RelationKind {
	case catalog.ForeignRelation:
		source, err := e.captureForeignSourceLocked(scope, ref, d, invoker, outputUses, mark)
		e.Mu.Unlock()
		return source, err
	case catalog.CatalogRelation:
		return e.openCatalogSourceLocked(scope, ref, d, keep)
	case catalog.SequenceRelation:
		source, err := e.captureSequenceSourceLocked(scope, ref, d)
		e.Mu.Unlock()
		return source, err
	case catalog.ViewRelation:
		for _, active := range scope.ViewCreationOrZero().Active {
			if active == d.Address() {
				e.Mu.Unlock()
				return nil, SQLError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.Name()))
			}
		}
		views := scope.viewCreation()
		views.Active = append(views.Active, d.Address())
		defer func() { views.Active = views.Active[:len(views.Active)-1] }()
		root, source, err := e.PrepareStoredQueryLocked(scope, ref, stored.ViewOf(d), invoker, mark, locks, outputUses)
		if err != nil {
			return nil, err
		}
		bridge := &exec.Source{Ref: ref, Authority: scope.authority, Locks: locks, NestedRoot: root, NestedInputs: source, Materialized: true, FlatWidth: len(root.Schema()), Inherited: mark}
		borrowQueryAuthority(bridge, scope)
		if locks != nil {
			occurrences := exec.QueryTreeOccurrences(root, source, nil)
			bridge.Lineage = &exec.QueryLockLineage{Root: root, Inputs: source, Stage: &exec.Source{Materialized: true, Occurrences: occurrences}}
		}
		return bridge, nil
	case catalog.PartitionedRelation:
		var leaves [1]exec.Leaf
		leaves[0] = exec.Leaf{Ref: ref, Relation: d}
		source, err := e.CaptureQuerySourcesForInvokerLocked(scope, leaves[:], false, invoker, mark, locks)
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err = exec.SelectQuerySources(ctx, source); err != nil {
			source.Close()
			return nil, err
		}
		return source, nil
	case catalog.IndexRelation:
		e.Mu.Unlock()
		return nil, SQLError("42809", fmt.Sprintf(`cannot read from index "%s"`, d.Name()))
	default:
		e.Mu.Unlock()
		return nil, fmt.Errorf("stuntgres: unsupported relation source")
	}
}

// Called before unlocked preflight and immediately before source capture. Each
// leaf checks current ACLs; an ordinary outer owner never replaces the invoker.
func (e *Engine) authorizeViewSourceLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark) (AuthorizationView, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return AuthorizationView{}, err
	}
	auth.Catalog = e.PrivateCatalogLocked(scope.PendingCatalog())
	auth.Recapture(auth.Catalog)
	return auth, view.AuthorizeSource(auth.View, auth, ref, definition, invoker, mark)
}
