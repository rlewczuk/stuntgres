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
	mark     queryRowMark
	query    *parser.Select
	table    *parser.TableReference
}

func newQueryLockAdmission(statement parser.Statement) (queryLockAdmission, []*parser.TableReference, []*parser.Select, error) {
	admissions := make(map[*parser.Select]*queryLockAdmission)
	var ordered []*queryLockAdmission
	tableQueries := make(map[*parser.TableReference]*parser.Select)
	admit := func(query *parser.Select) *queryLockAdmission {
		if prior := admissions[query]; prior != nil {
			return prior
		}
		marks, err := queryLockTargets(query.From, query.Locking)
		indices := make(map[*parser.TableReference]int)
		_ = parser.VisitQuerySources(query, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		admission := &queryLockAdmission{selectQuery: query, marks: marks, err: err, indices: indices}
		admissions[query] = admission
		ordered = append(ordered, admission)
		return admission
	}
	var tables []*parser.TableReference
	var queries []*parser.Select
	if err := parser.VisitQuerySources(statement, func(query *parser.Select) error { admit(query); return nil }, func(table *parser.TableReference, query *parser.Select) error {
		admit(query)
		tableQueries[table] = query
		tables = append(tables, table)
		queries = append(queries, query)
		return nil
	}); err != nil {
		return queryLockAdmission{}, nil, nil, err
	}
	return queryLockAdmission{queries: admissions, ordered: ordered, tableQueries: tableQueries}, tables, queries, nil
}

func (a *queryLockAdmission) tableMark(table *parser.TableReference) queryRowMark {
	mark := queryRowMark{}
	if a != nil {
		for _, admission := range a.ordered {
			if index, ok := admission.indices[table]; ok && index < len(admission.marks) {
				mark = mergeQueryRowMark(mark, admission.marks[index])
			}
		}
	}
	return mark
}

func (s *StatementScope) acquireQueryRelations(statement parser.Statement, authorityStmt parser.Statement, execute bool) error {
	admission, tables, queries, err := newQueryLockAdmission(statement)
	if err != nil {
		return err
	}
	s.queryLockAdmission = admission
	for i, table := range tables {
		mark := admission.tableMark(table)
		selection := &parser.Select{From: table}
		if mark.mode != 0 {
			selection.Locking = []parser.RowLockClause{{Mode: queryRowClause(mark.mode), Wait: mark.wait}}
		}
		if err := s.acquireRelationTarget(selection, authorityStmt, execute, true); err != nil {
			return err
		}
		leaf := &s.querySources[len(s.querySources)-1]
		leaf.mark, leaf.query, leaf.table = mark, queries[i], table
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
		if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage || leaf.mark != grant.mark {
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
	return e.captureQuerySourcesForInvokerLocked(scope, leaves, singleton, invoker, queryRowMark{}, scope.queryLocks)
}

// captureCommentSnapshotLocked shares one immutable catalog/role pair across
// query providers and completed-row CHECK evaluation. The caller holds e.mu.
func (e *Engine) captureCommentSnapshotLocked(scope *StatementScope) error {
	if scope.commentCatalog.state == nil {
		scope.commentCatalog = e.privateCatalogLocked(scope.owner.catalog)
		scope.commentRoles = RoleRead{e, e.roles}
	}
	if scope.deparsePath == nil {
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			return err
		}
		auth.catalog, auth.roles = scope.commentCatalog, scope.commentRoles
		scope.deparseAuth = auth
		scope.deparsePath, err = e.referencePath(auth)
		return err
	}
	return nil
}

func (e *Engine) captureCheckCommentSnapshotLocked(scope *StatementScope, relation *relationDescriptor) error {
	for _, definition := range relation.defaults {
		if definition.catalogSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	for _, check := range relation.checkConstraints {
		if check.commentSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) captureCheckVisibilitySnapshotLocked(scope *StatementScope, relation *relationDescriptor) error {
	for _, check := range relation.checkConstraints {
		if check.visibilitySnapshot {
			return e.captureVisibilitySnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) captureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []queryLeaf, singleton bool, invoker PrincipalRef, inherited queryRowMark, locks *queryLockContext) (*querySource, error) {
	if err := e.captureCommentSnapshotLocked(scope); err != nil {
		return nil, err
	}
	if scope.visibilityDemand {
		if err := e.captureVisibilitySnapshotLocked(scope); err != nil {
			return nil, err
		}
	}
	if len(leaves) == 0 {
		return &querySource{singleton: singleton, scope: scope, locks: locks}, nil
	}
	var single [1]ObjectRef
	refs := single[:0]
	if len(leaves) > 1 {
		refs = make([]ObjectRef, 0, len(leaves))
	}
	var collectErr error
	refs, collectErr = e.queryTableRefsLocked(scope.commentCatalog, leaves, refs)
	if collectErr != nil {
		return nil, collectErr
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
		mark := mergeQueryRowMark(inherited, leaf.mark)
		var source *querySource
		switch leaf.relation.relationKind {
		case sequenceRelation:
			source, err = e.captureSequenceSourceLocked(scope, leaf.ref, leaf.relation)
		case tableRelation:
			source, err = captureQueryPrefix(view, leaf.ref)
		default:
			// Recursive view opening consumes the lock, including failures.
			source, err = e.openRelationSourceLocked(scope, leaf.ref, invoker, mark, locks)
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
			locks.leaves = append(locks.leaves, queryLockLeaf{leaf: leaf, source: source, mark: mark})
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
		_, err := selectQuerySource(ctx, source, data.frontierAt(source.ref.Address), data.ends[source.ref.Address], data.deleted[source.ref.Address])
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
