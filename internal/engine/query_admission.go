package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) acquireQueryRelations(statement parser.Statement, authorityStmt parser.Statement, execute bool) error {
	admission, tables, queries, err := analyze.NewLockAdmission(statement)
	if err != nil {
		return err
	}
	return s.acquireAdmittedQueryRelations(admission, tables, queries, authorityStmt, execute)
}

func (s *StatementScope) acquireAdmittedQueryRelations(admission analyze.LockAdmission, tables []*parser.TableReference, queries []*parser.Select, authorityStmt parser.Statement, execute bool) error {
	s.queryLockAdmission = admission
	for i, table := range tables {
		mark := admission.TableMark(table)
		selection := &parser.Select{From: table}
		if mark.Mode != 0 {
			selection.Locking = []parser.RowLockClause{{Mode: analyze.RowLockClause(mark.Mode), Wait: mark.Wait}}
		}
		if s.prepared.reuse != nil {
			prior, ok := s.prepared.reuse.sources[table]
			if !ok || prior.Query != admission.RetainedQuery(table) {
				return ids.ErrCatalogReplaced
			}
			s.queryLockAdmission.TableQueries[table] = analyze.PreparedSource{Query: queries[i], Ref: prior.Ref}
		}
		if err := s.acquireRelationTarget(selection, authorityStmt, execute, true); err != nil {
			return analyze.FutureCTEError(err, admission.FutureCTE[table])
		}
		leaf := &s.querySources[len(s.querySources)-1]
		// Keep SQL marks in the admission manifest for exact binder validation.
		// pg_sequences drops only effective physical marks during source capture.
		leaf.Mark, leaf.Query, leaf.Table, leaf.Only = mark, queries[i], table, table.Only
		entry := s.queryLockAdmission.TableQueries[table]
		entry.Query, entry.Ref = queries[i], leaf.Ref
		s.queryLockAdmission.TableQueries[table] = entry
	}
	return nil
}

// Validate exact admission identity without resolving SQL names again. Metadata
// revisions may refresh authority before binding, never an already bound layout.
func currentQuerySourceLocked(e *Engine, catalogView catalog.Read, changes *session.Journal, source exec.Leaf) (*catalog.Relation, error) {
	current, ok := catalogView.Relation(source.Ref.Address)
	if !ok || source.Relation == nil || source.Ref.Domain() != e.catalog.Domain || source.Ref.Epoch != e.catalog.state.Epoch() || source.Ref.Address.SubID != 0 ||
		current.Address() != source.Relation.Address() || current.Namespace() != source.Relation.Namespace() || current.Name() != source.Relation.Name() || current.RelationKind != source.Relation.RelationKind ||
		!index.SameRelationRowShape(current, source.Relation) {
		return nil, ids.ErrCatalogReplaced
	}
	if current.RelationKind == catalog.SequenceRelation {
		if current.Sequence == nil || source.Storage != nil || current.SequencePlanStamp != source.Relation.SequencePlanStamp {
			return nil, ids.ErrCatalogReplaced
		}
	} else if storage := e.privateStorageLocked(changes, source.Ref.Address); storage != source.Storage &&
		!view.SameMaterializedLineage(source.Relation, current, source.Storage, storage) {
		return nil, ids.ErrCatalogReplaced
	}
	return current, nil
}

func (s *StatementScope) validateQuerySources(leaves []exec.Leaf) error {
	if len(s.mutations) != 0 && len(leaves) != len(s.querySources) {
		for _, grant := range s.querySources {
			found := false
			for _, leaf := range leaves {
				if leaf.Table != grant.Table {
					continue
				}
				found = true
				if leaf.Ref != grant.Ref || leaf.Relation != grant.Relation || leaf.Storage != grant.Storage || leaf.Mark != grant.Mark || leaf.Only != grant.Only {
					return ids.ErrCatalogReplaced
				}
				break
			}
			if !found {
				return ids.ErrCatalogReplaced
			}
		}
		e := s.engine()
		catalog := e.privateCatalogLocked(s.owner.Catalog)
		for _, leaf := range leaves {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.Catalog, leaf); err != nil {
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
		if leaf.Ref != grant.Ref || leaf.Relation != grant.Relation || leaf.Storage != grant.Storage || leaf.Mark != grant.Mark || leaf.Only != grant.Only {
			return ids.ErrCatalogReplaced
		}
	}
	if len(s.mutations) != 0 {
		s.querySources = leaves
	}
	return nil
}

// Called once with e.mu held: all table roots share this statement frontier.
func (e *Engine) captureQuerySourcesLocked(scope *StatementScope, leaves []exec.Leaf, singleton bool) (*exec.Source, error) {
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: hostOf(scope)}
	}
	var invoker authorization.PrincipalRef
	for _, leaf := range leaves {
		if leaf.Relation.RelationKind == catalog.ViewRelation {
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
	return s.engine()
}

// captureCommentSnapshotLocked shares one immutable catalog/role pair across
// query providers and completed-row CHECK evaluation. The caller holds e.mu.
func (e *Engine) captureCommentSnapshotLocked(scope *StatementScope) error {
	if scope.inquiry.commentCatalog.State() == nil {
		scope.inquiry.commentCatalog = e.privateCatalogLocked(scope.owner.Catalog)
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
		scope.inquiry.collationPath, err = e.lookupPath(auth, analyze.CollationLookup)
		if err != nil {
			return err
		}
	}
	scope.input.Regrole = regobj.RoleInput{Roles: scope.inquiry.commentRoles}
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

func (e *Engine) captureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []exec.Leaf, singleton bool, invoker authorization.PrincipalRef, inherited plan.RowMark, locks *exec.LockContext) (*exec.Source, error) {
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
		source := &exec.Source{Singleton: singleton, Locks: locks}
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
	refs, collectErr = e.queryTableRefsLocked(collectAuth, invoker, leaves, refs, scope.op.Session.SessionToken, inherited)
	if collectErr != nil {
		return nil, collectErr
	}
	var catalogView *visibility.View
	var err error
	needsView := len(refs) != 0
	if !needsView {
		for _, leaf := range leaves {
			if leaf.Relation.RelationKind == catalog.PartitionedRelation {
				needsView = true
				break
			}
		}
	}
	if needsView {
		catalogView, err = scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			return nil, err
		}
	}
	var first *exec.Source
	var sources []*exec.Source
	if len(leaves) > 1 {
		sources = make([]*exec.Source, len(leaves))
	}
	for i, leaf := range leaves {
		if leaf.AnalysisOnly {
			source := &exec.Source{Ref: leaf.Ref, Materialized: true, FlatWidth: len(leaf.Relation.Columns)}
			borrowQueryAuthority(source, scope)
			if first == nil {
				first = source
			}
			if sources != nil {
				sources[i] = source
			}
			continue
		}
		mark := exec.EffectiveQueryRowMark(leaf.Relation, exec.MergeQueryRowMark(inherited, leaf.Mark))
		var source *exec.Source
		command := catalog.PolicySelect
		if mark.Mode != 0 {
			command = catalog.PolicyUpdate
		}
		denied, securityErr := e.rowSecurityInputLocked(scope, leaf.Relation, leaf.Principal, command, mark.Mode != 0)
		if securityErr != nil {
			err = securityErr
		}
		if err != nil {
			e.closeQuerySourceSetLocked(scope.op.Context(), first, sources)
			return nil, err
		}
		if denied {
			source, err = view.CaptureDeniedQuerySource(catalogView, leaf.Ref)
		} else {
			switch leaf.Relation.RelationKind {
			case catalog.SequenceRelation:
				e.markTemporaryAccessLocked(scope, leaf.Ref)
				source, err = e.captureSequenceSourceLocked(scope, leaf.Ref, leaf.Relation)
			case catalog.MaterializedViewRelation:
				if !leaf.Relation.Populated {
					err = view.MaterializedViewReadError(leaf.Relation)
				} else {
					source, err = exec.CapturePrefix(catalogView, leaf.Ref)
				}
			case catalog.PartitionedRelation:
				if mark.Mode != 0 {
					source = nil
					err = sqlError("0A000", "row locking partition hierarchies is unsupported")
				} else if leaf.Only {
					// Empty virtual prefixes retain the same visibility authority
					// as ordinary hierarchy sources, without opening descendants.
					pin := catalogView.Retain()
					if pin == nil {
						err = fmt.Errorf("stuntgres: visibility view is released")
					} else {
						source = &exec.Source{Ref: leaf.Ref, View: pin, Inheritance: []exec.InheritanceQuerySegment{}}
					}
				} else {
					source, err = partition.CaptureQueryPrefix(scope.partitionCaptureCatalog(scope.inquiry.commentCatalog), catalogView, leaf.Ref, leaf.Relation)
				}
			case catalog.TableRelation:
				source, err = table.CaptureInheritanceQueryPrefix(scope.inheritanceRead(scope.inquiry.commentCatalog), catalogView, leaf, scope.op.Session.SessionToken)
				if err == nil {
					err = e.captureHashIndexesLocked(scope, leaf, source, mark)
					if err != nil {
						source.Close()
						source = nil
					} else {
						if source.Inheritance == nil {
							source.PartialMask = e.capturePartialMaskLocked(scope, leaf, catalogView, mark)
						}
					}
				}
			default:
				// Recursive view opening consumes the lock, including failures.
				// A nonnil empty slice means this occurrence consumes presence
				// only, unlike nil's full-projection callers.
				uses := leaf.Uses
				if uses == nil {
					uses = []int{}
				}
				sourceInvoker := invoker
				if scope.ruleAction.auth != nil && leaf.Principal != (authorization.PrincipalRef{}) {
					sourceInvoker = leaf.Principal
				}
				source, err = e.openRelationSourceLocked(scope, leaf.Ref, sourceInvoker, mark, locks, uses)
				e.mu.Lock()
			}
		}
		if err != nil {
			e.closeQuerySourceSetLocked(scope.op.Context(), first, sources)
			return nil, err
		}
		if !denied && (leaf.Relation.RelationKind == catalog.TableRelation || leaf.Relation.RelationKind == catalog.PartitionedRelation) {
			partition.CapturePruner(scope.partitionCaptureCatalog(scope.inheritanceRead(scope.inquiry.commentCatalog)), leaf.Relation, source)
		}
		borrowQueryAuthority(source, scope)
		source.Occurrence = i
		source.Locks = locks
		if locks != nil && source.Lineage == nil {
			source.Occurrence = len(locks.Leaves)
			locks.Leaves = append(locks.Leaves, exec.QueryLockLeaf{Leaf: leaf, Source: source, Mark: mark})
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
	first.Sources = sources
	return first, nil
}

// Source teardown can enter foreign provider code. Detach the captured set
// while locked, then release all provider resources outside Engine.mu.
func (e *Engine) closeQuerySourceSetLocked(ctx context.Context, first *exec.Source, sources []*exec.Source) {
	e.mu.Unlock()
	if first != nil {
		_ = exec.FinishForeignSources(ctx, first)
		first.Close()
	}
	for _, source := range sources {
		if source != nil && source != first {
			_ = exec.FinishForeignSources(ctx, source)
			source.Close()
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
func (e *Engine) captureHashIndexesLocked(scope *StatementScope, leaf exec.Leaf, source *exec.Source, mark plan.RowMark) error {
	if !scope.implicit || scope.catalog != nil || mark.Mode != 0 || source.View == nil || source.Inheritance != nil || len(source.Own) != 0 {
		return nil
	}
	data := source.View.Data()
	if data.Catalog.State() != e.catalog.state {
		return nil
	}
	a := leaf.Ref.Address
	store := data.GenerationAt(a)
	if store == nil || store != e.storage.Relations[a] || data.FrontierAt(a) != e.visibility.CommitSeq() || data.Deleted[a] != nil || len(source.Committed) != len(store.Versions) || data.Ends[a] != store.Ends {
		return nil
	}
	for address, carrier := range e.storage.IndexRoots {
		idx, ok := data.Catalog.Relation(address)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Method != catalog.HashMethod || idx.SQLIndex.Parent != a || carrier == nil || carrier.Hash == nil {
			continue
		}
		// Readiness permits maintenance, not complete query coverage. A
		// grandfathered commit may legitimately be absent from an invalid root.
		if !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live {
			continue
		}
		live, ok := e.catalog.state.Relation(address)
		if !ok || !index.SameIndexKeySemantics(idx, live) {
			continue
		}
		root := carrier.Hash
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(source.Committed) || root.Ends != data.Ends[a] {
			continue
		}
		usage, err := index.KeyUsage(leaf.Relation, idx.SQLIndex.Keys[0])
		if err != nil {
			return err
		}
		if usage != root.Binding.Usage {
			continue
		}
		source.HashIndexes = append(source.HashIndexes, exec.CapturedHashIndex{Definition: idx.SQLIndex, Root: root, Usage: usage})
	}
	return nil
}

func borrowQueryAuthority(s *exec.Source, scope *StatementScope) {
	s.Authority = scope.authority
	s.Host = nil
	if s.Authority == nil {
		s.Host = hostOf(scope)
	}
}

func (e *Engine) captureSequenceSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation) (*exec.Source, error) {
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return nil, fmt.Errorf("stuntgres: sequence state missing")
	}
	i := relation.Columns[0].Usage.Type
	last, _ := sqltype.Integer(i, g.Last)
	log, _ := sqltype.Integer(i, 0)
	return &exec.Source{Ref: ref, Anonymous: [][]sqltype.Value{{last, log, sqltype.Boolean(relation.Columns[2].Usage.Type, g.Called)}}}, nil
}
