package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
)

// Each manifest entry is an SQL occurrence, even when physical identity repeats.
type queryLeaf struct {
	ref      ObjectRef
	relation *relationDescriptor
	storage  *relationStorage
	uses     []int
}

func (s *StatementScope) acquireQueryRelations(sel *parser.Select, authorityStmt parser.Statement, execute bool) error {
	if sel.Locking != nil {
		s.queryLockMode = queryRowMode(sel.Locking.Mode)
	}
	for _, table := range parser.FromTables(sel.From) {
		// Keep the enclosing CREATE VIEW authority for each leaf, including
		// aliased and joined sources. The leaf SELECT identifies its lock mode.
		if err := s.acquireRelationTarget(&parser.Select{From: table, Locking: sel.Locking}, authorityStmt, execute, true); err != nil {
			return err
		}
	}
	return nil
}

// Validate exact admission identity without resolving SQL names again. Metadata
// revisions may refresh authority before binding, never an already bound layout.
func currentQuerySourceLocked(e *Engine, catalog CatalogRead, changes *catalogChanges, source queryLeaf) (*relationDescriptor, error) {
	current, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
	if !ok || source.relation == nil || source.ref.owner != e.owner || source.ref.Epoch != e.state.epoch || source.ref.Address.SubID != 0 ||
		current.address != source.relation.address || current.namespace != source.relation.namespace || current.name != source.relation.name || current.relationKind != source.relation.relationKind ||
		!sameRelationRowShape(current, source.relation) {
		return nil, ErrCatalogReplaced
	}
	if current.relationKind == sequenceRelation {
		if current.sequence == nil || source.storage != nil || current.sequencePlanStamp != source.relation.sequencePlanStamp {
			return nil, ErrCatalogReplaced
		}
	} else if e.privateStorageLocked(changes, source.ref.Address) != source.storage {
		return nil, ErrCatalogReplaced
	}
	return current, nil
}

func (s *StatementScope) validateQuerySources(leaves []queryLeaf) error {
	if len(leaves) != len(s.querySources) {
		return ErrCatalogReplaced
	}
	for i, leaf := range leaves {
		grant := s.querySources[i]
		if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage {
			return ErrCatalogReplaced
		}
	}
	return nil
}

// Called once with e.mu held: all table roots share this statement frontier.
func (e *Engine) captureQuerySourcesLocked(scope *StatementScope, leaves []queryLeaf, singleton bool) (*querySource, error) {
	var invoker PrincipalRef
	for _, leaf := range leaves {
		if leaf.relation.view != nil {
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				return nil, err
			}
			invoker = auth.state.CurrentUser
			break
		}
	}
	mode := scope.queryLockMode
	return e.captureQuerySourcesForInvokerLocked(scope, leaves, singleton, invoker, mode, scope.queryLocks)
}

func (e *Engine) captureCommentSnapshotLocked(scope *StatementScope) {
	if scope.commentCatalog.state == nil {
		scope.commentCatalog = e.privateCatalogLocked(scope.owner.catalog)
		scope.commentRoles = RoleRead{e, e.roles}
	}
}

func (e *Engine) captureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []queryLeaf, singleton bool, invoker PrincipalRef, mode relationLockMode, locks *queryLockContext) (*querySource, error) {
	e.captureCommentSnapshotLocked(scope)
	if len(leaves) == 0 {
		return &querySource{singleton: singleton, scope: scope, locks: locks}, nil
	}
	var single [1]ObjectRef
	refs := single[:0]
	if len(leaves) > 1 {
		refs = make([]ObjectRef, 0, len(leaves))
	}
	catalog := scope.commentCatalog
	var visited map[ObjectRef]bool
	var collect func(ObjectRef) error
	collect = func(ref ObjectRef) error {
		relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || ref.owner != e.owner || ref.Epoch != catalog.Epoch() {
			return ErrCatalogReplaced
		}
		if relation.view != nil {
			if visited[ref] {
				return nil
			}
			if visited == nil {
				visited = make(map[ObjectRef]bool)
			}
			visited[ref] = true
			for _, child := range relation.view.sources {
				if err := collect(child.ref); err != nil {
					return err
				}
			}
		}
		if relation.relationKind == tableRelation {
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
		if err := collect(leaf.ref); err != nil {
			return nil, err
		}
	}
	var view *VisibilityView
	var err error
	if len(refs) != 0 {
		view, err = scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			return nil, err
		}
	}
	var first *querySource
	var sources []*querySource
	if len(leaves) > 1 {
		sources = make([]*querySource, len(leaves))
	}
	for i, leaf := range leaves {
		var source *querySource
		switch leaf.relation.relationKind {
		case sequenceRelation:
			source, err = e.captureSequenceSourceLocked(scope, leaf.ref, leaf.relation)
		case tableRelation:
			source, err = captureQueryPrefix(view, leaf.ref)
		default:
			// Recursive view opening consumes the lock, including failures.
			source, err = e.openRelationSourceLocked(scope, leaf.ref, invoker, mode, locks)
			e.mu.Lock()
		}
		if err != nil {
			if first != nil {
				first.close()
			}
			for _, prior := range sources {
				if prior != nil {
					prior.close()
				}
			}
			return nil, err
		}
		source.scope = scope
		source.occurrence = i
		source.locks = locks
		if locks != nil && source.lineage == nil {
			source.occurrence = len(locks.leaves)
			locks.leaves = append(locks.leaves, queryLockLeaf{leaf: leaf, source: source, mode: mode})
		}
		if i == 0 {
			first = source
		}
		if sources != nil {
			sources[i] = source
		}
	}
	if first == nil {
		return nil, fmt.Errorf("stuntgres: missing query source manifest")
	}
	first.sources = sources
	return first, nil
}

func selectQuerySources(ctx context.Context, source *querySource) error {
	selectOne := func(source *querySource) error {
		if source.view == nil {
			return ctx.Err()
		}
		data := source.view.read()
		_, err := selectQuerySource(ctx, source, data.frontier, data.ends[source.ref.Address], data.deleted[source.ref.Address])
		return err
	}
	if source.sources == nil {
		if err := selectOne(source); err != nil {
			source.close()
			return err
		}
		return nil
	}
	for _, leaf := range source.sources {
		if err := selectOne(leaf); err != nil {
			source.close()
			return err
		}
	}
	return nil
}
