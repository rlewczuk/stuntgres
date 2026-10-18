package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

// Each manifest entry is an SQL occurrence, even when physical identity repeats.
type queryLeaf struct {
	ref              ObjectRef
	relation         *relationDescriptor
	storage          *relationStorage
	only             bool
	principal        authorization.PrincipalRef // Relation authority, not expression/session identity.
	frozenAttributes []int32                    // Execution-owned stored-view subquery layout.
	uses             []int
	mark             queryRowMark
	query            *parser.Select
	table            *parser.TableReference
	// DELETE's immediate USING occurrences require reference-kind validation,
	// not SELECT FOR UPDATE authority or source tuple locks.
	deleteReference  bool
	partialCandidate *relationDescriptor
	analysisOnly     bool // Unreferenced CTE body: analyzed dependency, no executable ACL.
}

func newQueryLockAdmission(statement parser.Statement) (queryLockAdmission, []*parser.TableReference, []*parser.Select, error) {
	admissions := make(map[*parser.Select]*queryLockAdmission)
	var ordered []*queryLockAdmission
	tableQueries := make(map[*parser.TableReference]preparedSource)
	future := make(map[*parser.TableReference]string)
	virtual := make(map[*parser.TableReference]bool)
	admit := func(query *parser.Select) *queryLockAdmission {
		if prior := admissions[query]; prior != nil {
			return prior
		}
		indices := make(map[*parser.TableReference]int)
		_ = parser.VisitQuerySources(&parser.Select{From: query.From}, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		admission := &queryLockAdmission{selectQuery: query, indices: indices}
		admissions[query] = admission
		ordered = append(ordered, admission)
		return admission
	}
	var tables []*parser.TableReference
	var queries []*parser.Select
	if err := parser.VisitQuerySources(statement, func(query *parser.Select) error { admit(query); return nil }, func(table *parser.TableReference, query *parser.Select) error {
		if admissions[query] == nil {
			admit(query).syntheticOwner = true
		}
		tableQueries[table] = preparedSource{query: query}
		tables = append(tables, table)
		queries = append(queries, query)
		return nil
	}, func(table *parser.TableReference, isVirtual bool, pending string) {
		virtual[table] = isVirtual
		future[table] = pending
	}); err != nil {
		return queryLockAdmission{}, nil, nil, err
	}
	for _, a := range ordered {
		a.marks, a.err = queryLockTargets(a.selectQuery.From, a.selectQuery.Locking, virtual)
	}
	return queryLockAdmission{queries: admissions, ordered: ordered, tableQueries: tableQueries, futureCTE: future, virtualCTE: virtual}, tables, queries, nil
}

// The original table pointer is stable for every occurrence. Only real SELECT
// syntax has a stable owning query; the parser regenerates DML source roots.
func (a *queryLockAdmission) retainedQuery(table *parser.TableReference) *parser.Select {
	query := a.tableQueries[table].query
	if owner := a.queries[query]; owner != nil && owner.syntheticOwner {
		return nil
	}
	return query
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
	return s.acquireAdmittedQueryRelations(admission, tables, queries, authorityStmt, execute)
}

func (s *StatementScope) acquireAdmittedQueryRelations(admission queryLockAdmission, tables []*parser.TableReference, queries []*parser.Select, authorityStmt parser.Statement, execute bool) error {
	s.queryLockAdmission = admission
	for i, table := range tables {
		mark := admission.tableMark(table)
		selection := &parser.Select{From: table}
		if mark.mode != 0 {
			selection.Locking = []parser.RowLockClause{{Mode: queryRowClause(mark.mode), Wait: mark.wait}}
		}
		if s.preparedReuse != nil {
			prior, ok := s.preparedReuse.sources[table]
			if !ok || prior.query != admission.retainedQuery(table) {
				return ErrCatalogReplaced
			}
			s.queryLockAdmission.tableQueries[table] = preparedSource{query: queries[i], ref: prior.ref}
		}
		if err := s.acquireRelationTarget(selection, authorityStmt, execute, true); err != nil {
			return futureCTEError(err, admission.futureCTE[table])
		}
		leaf := &s.querySources[len(s.querySources)-1]
		// Keep SQL marks in the admission manifest for exact binder validation.
		// pg_sequences drops only effective physical marks during source capture.
		leaf.mark, leaf.query, leaf.table, leaf.only = mark, queries[i], table, table.Only
		entry := s.queryLockAdmission.tableQueries[table]
		entry.query, entry.ref = queries[i], leaf.ref
		s.queryLockAdmission.tableQueries[table] = entry
	}
	return nil
}

// Validate exact admission identity without resolving SQL names again. Metadata
// revisions may refresh authority before binding, never an already bound layout.
func currentQuerySourceLocked(e *Engine, catalog CatalogRead, changes *catalogChanges, source queryLeaf) (*relationDescriptor, error) {
	current, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
	if !ok || source.relation == nil || source.ref.owner != e.domain || source.ref.Epoch != e.state.epoch || source.ref.Address.SubID != 0 ||
		current.address != source.relation.address || current.namespace != source.relation.namespace || current.name != source.relation.name || current.relationKind != source.relation.relationKind ||
		!sameRelationRowShape(current, source.relation) {
		return nil, ErrCatalogReplaced
	}
	if current.relationKind == sequenceRelation {
		if current.sequence == nil || source.storage != nil || current.sequencePlanStamp != source.relation.sequencePlanStamp {
			return nil, ErrCatalogReplaced
		}
	} else if storage := e.privateStorageLocked(changes, source.ref.Address); storage != source.storage &&
		!sameMaterializedLineage(source.relation, current, source.storage, storage) {
		return nil, ErrCatalogReplaced
	}
	return current, nil
}

func (s *StatementScope) validateQuerySources(leaves []queryLeaf) error {
	if len(s.mutations) != 0 && len(leaves) != len(s.querySources) {
		for _, grant := range s.querySources {
			found := false
			for _, leaf := range leaves {
				if leaf.table != grant.table {
					continue
				}
				found = true
				if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage || leaf.mark != grant.mark || leaf.only != grant.only {
					return ErrCatalogReplaced
				}
				break
			}
			if !found {
				return ErrCatalogReplaced
			}
		}
		e := s.op.session.Engine()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		for _, leaf := range leaves {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, leaf); err != nil {
				return err
			}
		}
		s.querySources = leaves
		return nil
	}
	if len(leaves) != len(s.querySources) {
		return ErrCatalogReplaced
	}
	for i, leaf := range leaves {
		grant := s.querySources[i]
		if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage || leaf.mark != grant.mark || leaf.only != grant.only {
			return ErrCatalogReplaced
		}
	}
	if len(s.mutations) != 0 {
		s.querySources = leaves
	}
	return nil
}

// Called once with e.mu held: all table roots share this statement frontier.
func (e *Engine) captureQuerySourcesLocked(scope *StatementScope, leaves []queryLeaf, singleton bool) (*querySource, error) {
	if scope.authority == nil {
		scope.authority = &queryAuthority{scope: scope}
	}
	var invoker authorization.PrincipalRef
	for _, leaf := range leaves {
		if leaf.relation.relationKind == viewRelation {
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
		scope.commentRoles = e.roleRead(scope.commentCatalog, e.operationRolesLocked(scope.op))
	}
	scope.output.ACLItem = scope
	scope.output.Regrole = scope
	scope.output.Operator = scope
	scope.output.Regcollation, scope.output.Regnamespace = scope, scope
	scope.output.RegTS = scope
	if scope.deparsePath == nil {
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			return err
		}
		auth.recaptureCatalog(scope.commentCatalog)
		scope.commentRoles = auth.roles
		scope.deparseAuth = auth
		scope.deparsePath, err = e.referencePath(auth)
		if err != nil {
			return err
		}
		scope.collationPath, err = e.lookupPath(auth, collationLookup)
		if err != nil {
			return err
		}
	}
	scope.input.Regrole = capturedRegroleInput{roles: scope.commentRoles}
	input := captureRegObjectsOnPath(scope.deparseAuth, scope.collationPath)
	scope.input.Regcollation, scope.input.Regnamespace = input, input
	return nil
}

func (e *Engine) captureCheckCommentSnapshotLocked(scope *StatementScope, relation *relationDescriptor) error {
	for _, column := range relation.columns {
		if column.Usage.Type.ContainsRegclass() || column.Usage.Type.ContainsOperatorAlias() || column.Usage.Type.ContainsACLItem() || column.Usage.Type.ContainsRegObject() || column.Usage.Type.ContainsRegTS() {
			if err := e.captureCommentSnapshotLocked(scope); err != nil {
				return err
			}
			scope.output.Regclass = scope
			return nil
		}
	}
	for _, definition := range relation.defaults {
		if definition.catalogSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	for _, definition := range relation.generations {
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

func (e *Engine) captureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []queryLeaf, singleton bool, invoker authorization.PrincipalRef, inherited queryRowMark, locks *queryLockContext) (*querySource, error) {
	// Metadata-only retained captures have no admitted transaction owner.
	// Singleton execution still advertises its actual live statement snapshot.
	if scope.txRef != (TxRef{}) {
		if _, err := e.visibilitySnapshotLocked(scope.txRef, false); err != nil {
			return nil, err
		}
	}
	if err := e.captureCommentSnapshotLocked(scope); err != nil {
		return nil, err
	}
	if scope.visibilityDemand {
		if err := e.captureVisibilitySnapshotLocked(scope); err != nil {
			return nil, err
		}
	}
	if len(leaves) == 0 {
		source := &querySource{singleton: singleton, locks: locks}
		source.borrowAuthority(scope)
		return source, nil
	}
	var single [1]ObjectRef
	refs := single[:0]
	if len(leaves) > 1 {
		refs = make([]ObjectRef, 0, len(leaves))
	}
	var collectErr error
	collectAuth := scope.deparseAuth
	collectAuth.recaptureCatalog(scope.partitionCaptureCatalog(scope.inheritanceRead(scope.commentCatalog)))
	refs, collectErr = e.queryTableRefsLocked(collectAuth, invoker, leaves, refs, scope.op.session.token, inherited)
	if collectErr != nil {
		return nil, collectErr
	}
	var view *VisibilityView
	var err error
	needsView := len(refs) != 0
	if !needsView {
		for _, leaf := range leaves {
			if leaf.relation.relationKind == partitionedRelation {
				needsView = true
				break
			}
		}
	}
	if needsView {
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
		if leaf.analysisOnly {
			source := &querySource{ref: leaf.ref, materialized: true, flatWidth: len(leaf.relation.columns)}
			source.borrowAuthority(scope)
			if first == nil {
				first = source
			}
			if sources != nil {
				sources[i] = source
			}
			continue
		}
		mark := effectiveQueryRowMark(leaf.relation, mergeQueryRowMark(inherited, leaf.mark))
		var source *querySource
		command := policySelect
		if mark.mode != 0 {
			command = policyUpdate
		}
		denied, securityErr := e.rowSecurityInputLocked(scope, leaf.relation, leaf.principal, command, mark.mode != 0)
		if securityErr != nil {
			err = securityErr
		}
		if err != nil {
			e.closeQuerySourceSetLocked(scope.op.Context(), first, sources)
			return nil, err
		}
		if denied {
			source, err = captureDeniedQuerySource(view, leaf.ref)
		} else {
			switch leaf.relation.relationKind {
			case sequenceRelation:
				e.markTemporaryAccessLocked(scope, leaf.ref)
				source, err = e.captureSequenceSourceLocked(scope, leaf.ref, leaf.relation)
			case materializedViewRelation:
				if !leaf.relation.populated {
					err = materializedViewReadError(leaf.relation)
				} else {
					source, err = captureQueryPrefix(view, leaf.ref)
				}
			case partitionedRelation:
				if mark.mode != 0 {
					source = nil
					err = sqlError("0A000", "row locking partition hierarchies is unsupported")
				} else if leaf.only {
					// Empty virtual prefixes retain the same visibility authority
					// as ordinary hierarchy sources, without opening descendants.
					pin := view.Retain()
					if pin == nil {
						err = fmt.Errorf("stuntgres: visibility view is released")
					} else {
						source = &querySource{ref: leaf.ref, view: pin, inheritance: []inheritanceQuerySegment{}}
					}
				} else {
					source, err = capturePartitionQueryPrefix(scope.partitionCaptureCatalog(scope.commentCatalog), view, leaf.ref, leaf.relation)
				}
			case tableRelation:
				source, err = captureInheritanceQueryPrefix(scope.inheritanceRead(scope.commentCatalog), view, leaf, scope.op.session.token)
				if err == nil {
					err = e.captureHashIndexesLocked(scope, leaf, source, mark)
					if err != nil {
						source.close()
						source = nil
					} else {
						if source.inheritance == nil {
							source.partialMask = e.capturePartialMaskLocked(scope, leaf, view, mark)
						}
					}
				}
			default:
				// Recursive view opening consumes the lock, including failures.
				// A nonnil empty slice means this occurrence consumes presence
				// only, unlike nil's full-projection callers.
				uses := leaf.uses
				if uses == nil {
					uses = []int{}
				}
				sourceInvoker := invoker
				if scope.ruleActionAuth != nil && leaf.principal != (authorization.PrincipalRef{}) {
					sourceInvoker = leaf.principal
				}
				source, err = e.openRelationSourceLocked(scope, leaf.ref, sourceInvoker, mark, locks, uses)
				e.mu.Lock()
			}
		}
		if err != nil {
			e.closeQuerySourceSetLocked(scope.op.Context(), first, sources)
			return nil, err
		}
		source.borrowAuthority(scope)
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
		if source.inheritance != nil {
			return source.selectInheritancePrefixes(ctx)
		}
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

// Source teardown can enter foreign provider code. Detach the captured set
// while locked, then release all provider resources outside Engine.mu.
func (e *Engine) closeQuerySourceSetLocked(ctx context.Context, first *querySource, sources []*querySource) {
	e.mu.Unlock()
	if first != nil {
		_ = finishForeignSources(ctx, first)
		first.close()
	}
	for _, source := range sources {
		if source != nil && source != first {
			_ = finishForeignSources(ctx, source)
			source.close()
		}
	}
	e.mu.Lock()
}
