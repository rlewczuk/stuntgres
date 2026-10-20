package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

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

func (a *queryLockAdmission) tableMark(table *parser.TableReference) plan.RowMark {
	mark := plan.RowMark{}
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
		if mark.Mode != 0 {
			selection.Locking = []parser.RowLockClause{{Mode: queryRowClause(mark.Mode), Wait: mark.Wait}}
		}
		if s.prepared.reuse != nil {
			prior, ok := s.prepared.reuse.sources[table]
			if !ok || prior.query != admission.retainedQuery(table) {
				return ids.ErrCatalogReplaced
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
func currentQuerySourceLocked(e *Engine, view catalog.Read, changes *catalogChanges, source queryLeaf) (*catalog.Relation, error) {
	current, ok := view.Relation(source.ref.Address)
	if !ok || source.relation == nil || source.ref.Domain() != e.catalog.Domain || source.ref.Epoch != e.catalog.state.Epoch() || source.ref.Address.SubID != 0 ||
		current.Address() != source.relation.Address() || current.Namespace() != source.relation.Namespace() || current.Name() != source.relation.Name() || current.RelationKind != source.relation.RelationKind ||
		!sameRelationRowShape(current, source.relation) {
		return nil, ids.ErrCatalogReplaced
	}
	if current.RelationKind == catalog.SequenceRelation {
		if current.Sequence == nil || source.storage != nil || current.SequencePlanStamp != source.relation.SequencePlanStamp {
			return nil, ids.ErrCatalogReplaced
		}
	} else if storage := e.privateStorageLocked(changes, source.ref.Address); storage != source.storage &&
		!sameMaterializedLineage(source.relation, current, source.storage, storage) {
		return nil, ids.ErrCatalogReplaced
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
					return ids.ErrCatalogReplaced
				}
				break
			}
			if !found {
				return ids.ErrCatalogReplaced
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
		return ids.ErrCatalogReplaced
	}
	for i, leaf := range leaves {
		grant := s.querySources[i]
		if leaf.ref != grant.ref || leaf.relation != grant.relation || leaf.storage != grant.storage || leaf.mark != grant.mark || leaf.only != grant.only {
			return ids.ErrCatalogReplaced
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
		scope.authority = &queryAuthority{host: hostOf(scope)}
	}
	var invoker authorization.PrincipalRef
	for _, leaf := range leaves {
		if leaf.relation.RelationKind == catalog.ViewRelation {
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				return nil, err
			}
			invoker = auth.State.CurrentUser
			break
		}
	}
	return e.captureQuerySourcesForInvokerLocked(scope, leaves, singleton, invoker, plan.RowMark{}, scope.queryLocks)
}

// commentEngine returns the engine whose catalog the comment snapshot was
// captured from, or nil before capture.
func (s *StatementScope) commentEngine() *Engine {
	if s.inquiry.commentCatalog.Owner() == nil {
		return nil
	}
	return s.op.session.Engine()
}

// captureCommentSnapshotLocked shares one immutable catalog/role pair across
// query providers and completed-row CHECK evaluation. The caller holds e.mu.
func (e *Engine) captureCommentSnapshotLocked(scope *StatementScope) error {
	if scope.inquiry.commentCatalog.State() == nil {
		scope.inquiry.commentCatalog = e.privateCatalogLocked(scope.owner.catalog)
		scope.inquiry.commentRoles = e.roles.read(e.catalog.owner(), scope.inquiry.commentCatalog, e.operationRolesLocked(scope.op))
	}
	scope.output.ACLItem = scope
	scope.output.Regrole = scope
	scope.output.Operator = scope
	scope.output.Regcollation, scope.output.Regnamespace = scope, scope
	scope.output.RegTS = scope
	if scope.inquiry.deparsePath == nil {
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			return err
		}
		auth.Recapture(scope.inquiry.commentCatalog)
		scope.inquiry.commentRoles = auth.Roles
		scope.inquiry.deparseAuth = auth
		scope.inquiry.deparsePath, err = e.referencePath(auth)
		if err != nil {
			return err
		}
		scope.inquiry.collationPath, err = e.lookupPath(auth, collationLookup)
		if err != nil {
			return err
		}
	}
	scope.input.Regrole = capturedRegroleInput{roles: scope.inquiry.commentRoles}
	input := captureRegObjectsOnPath(scope.inquiry.deparseAuth, scope.inquiry.collationPath)
	scope.input.Regcollation, scope.input.Regnamespace = input, input
	return nil
}

func (e *Engine) captureCheckCommentSnapshotLocked(scope *StatementScope, relation *catalog.Relation) error {
	for _, column := range relation.Columns {
		if column.Usage.Type.ContainsRegclass() || column.Usage.Type.ContainsOperatorAlias() || column.Usage.Type.ContainsACLItem() || column.Usage.Type.ContainsRegObject() || column.Usage.Type.ContainsRegTS() {
			if err := e.captureCommentSnapshotLocked(scope); err != nil {
				return err
			}
			scope.output.Regclass = scope
			return nil
		}
	}
	for _, definition := range relation.ColumnMeta.Defaults {
		if definition.CatalogSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	for _, definition := range relation.ColumnMeta.Generations {
		if definition.CatalogSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	for _, check := range relation.Constraints.Check {
		if check.CommentSnapshot {
			return e.captureCommentSnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) captureCheckVisibilitySnapshotLocked(scope *StatementScope, relation *catalog.Relation) error {
	for _, check := range relation.Constraints.Check {
		if check.VisibilitySnapshot {
			return e.captureVisibilitySnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) captureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []queryLeaf, singleton bool, invoker authorization.PrincipalRef, inherited plan.RowMark, locks *queryLockContext) (*querySource, error) {
	// Metadata-only retained captures have no admitted transaction owner.
	// Singleton execution still advertises its actual live statement snapshot.
	if scope.txRef != (ids.TxRef{}) {
		if _, err := e.visibilitySnapshotLocked(scope.txRef, false); err != nil {
			return nil, err
		}
	}
	if err := e.captureCommentSnapshotLocked(scope); err != nil {
		return nil, err
	}
	if scope.inquiry.visibilityDemand {
		if err := e.captureVisibilitySnapshotLocked(scope); err != nil {
			return nil, err
		}
	}
	if len(leaves) == 0 {
		source := &querySource{singleton: singleton, locks: locks}
		borrowQueryAuthority(source, scope)
		return source, nil
	}
	var single [1]ids.ObjectRef
	refs := single[:0]
	if len(leaves) > 1 {
		refs = make([]ids.ObjectRef, 0, len(leaves))
	}
	var collectErr error
	collectAuth := scope.inquiry.deparseAuth
	collectAuth.Recapture(scope.partitionCaptureCatalog(scope.inheritanceRead(scope.inquiry.commentCatalog)))
	refs, collectErr = e.queryTableRefsLocked(collectAuth, invoker, leaves, refs, scope.op.session.token, inherited)
	if collectErr != nil {
		return nil, collectErr
	}
	var view *visibility.View
	var err error
	needsView := len(refs) != 0
	if !needsView {
		for _, leaf := range leaves {
			if leaf.relation.RelationKind == catalog.PartitionedRelation {
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
			source := &querySource{ref: leaf.ref, materialized: true, flatWidth: len(leaf.relation.Columns)}
			borrowQueryAuthority(source, scope)
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
		command := catalog.PolicySelect
		if mark.Mode != 0 {
			command = catalog.PolicyUpdate
		}
		denied, securityErr := e.rowSecurityInputLocked(scope, leaf.relation, leaf.principal, command, mark.Mode != 0)
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
			switch leaf.relation.RelationKind {
			case catalog.SequenceRelation:
				e.markTemporaryAccessLocked(scope, leaf.ref)
				source, err = e.captureSequenceSourceLocked(scope, leaf.ref, leaf.relation)
			case catalog.MaterializedViewRelation:
				if !leaf.relation.Populated {
					err = materializedViewReadError(leaf.relation)
				} else {
					source, err = captureQueryPrefix(view, leaf.ref)
				}
			case catalog.PartitionedRelation:
				if mark.Mode != 0 {
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
					source, err = capturePartitionQueryPrefix(scope.partitionCaptureCatalog(scope.inquiry.commentCatalog), view, leaf.ref, leaf.relation)
				}
			case catalog.TableRelation:
				source, err = captureInheritanceQueryPrefix(scope.inheritanceRead(scope.inquiry.commentCatalog), view, leaf, scope.op.session.token)
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
		borrowQueryAuthority(source, scope)
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

// inquirySnapshot is the statement's catalog-inquiry snapshot: comment, deparse,
// visibility and namespace reads captured once per statement. It is a value
// group, not a pointer slot, so capturing it allocates nothing.
type inquirySnapshot struct {
	commentCatalog         catalog.Read
	commentRoles           authorization.Read
	deparseAuth            AuthorizationView
	deparsePath            []ids.ObjectAddress
	collationPath          []ids.ObjectAddress
	visibilityCatalog      catalog.Read
	visibilityRelationPath []ids.ObjectAddress
	visibilityOtherPath    []ids.ObjectAddress
	visibilityDemand       bool
	currentSchemaResult    sqltype.Value
	searchPathResults      [2]sqltype.Value
}

// The caller holds e.mu, the same owner as visibility-prefix capture. After
// this proof, probing consults only the retained root and retained source rows.
func (e *Engine) captureHashIndexesLocked(scope *StatementScope, leaf queryLeaf, source *querySource, mark plan.RowMark) error {
	if !scope.implicit || scope.catalog != nil || mark.Mode != 0 || source.view == nil || source.inheritance != nil || len(source.own) != 0 {
		return nil
	}
	data := source.view.Data()
	if data.Catalog.State() != e.catalog.state {
		return nil
	}
	a := leaf.ref.Address
	store := data.GenerationAt(a)
	if store == nil || store != e.storage.Relations[a] || data.FrontierAt(a) != e.visibility.CommitSeq() || data.Deleted[a] != nil || len(source.committed) != len(store.Versions) || data.Ends[a] != store.Ends {
		return nil
	}
	for address, carrier := range e.storage.IndexRoots {
		index, ok := data.Catalog.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Method != catalog.HashMethod || index.SQLIndex.Parent != a || carrier == nil || carrier.Hash == nil {
			continue
		}
		// Readiness permits maintenance, not complete query coverage. A
		// grandfathered commit may legitimately be absent from an invalid root.
		if !index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live {
			continue
		}
		live, ok := e.catalog.state.Relation(address)
		if !ok || !sameIndexKeySemantics(index, live) {
			continue
		}
		root := carrier.Hash
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(source.committed) || root.Ends != data.Ends[a] {
			continue
		}
		usage, err := indexKeyUsage(leaf.relation, index.SQLIndex.Keys[0])
		if err != nil {
			return err
		}
		if usage != root.Binding.Usage {
			continue
		}
		source.hashIndexes = append(source.hashIndexes, capturedHashIndex{definition: index.SQLIndex, root: root, usage: usage})
	}
	return nil
}

func borrowQueryAuthority(s *querySource, scope *StatementScope) {
	s.authority = scope.authority
	s.host = nil
	if s.authority == nil {
		s.host = hostOf(scope)
	}
}

func (e *Engine) captureSequenceSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation) (*querySource, error) {
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return nil, fmt.Errorf("stuntgres: sequence state missing")
	}
	i := relation.Columns[0].Usage.Type
	last, _ := sqltype.Integer(i, g.Last)
	log, _ := sqltype.Integer(i, 0)
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{{last, log, sqltype.Boolean(relation.Columns[2].Usage.Type, g.Called)}}}, nil
}
