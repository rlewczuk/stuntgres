package core

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

func (s *StatementScope) AcquireQueryRelations(statement parser.Statement, authorityStmt parser.Statement, execute bool) error {
	admission, tables, queries, err := analyze.NewLockAdmission(statement)
	if err != nil {
		return err
	}
	return s.acquireAdmittedQueryRelations(admission, tables, queries, authorityStmt, execute)
}

func (s *StatementScope) acquireAdmittedQueryRelations(admission analyze.LockAdmission, tables []*parser.TableReference, queries []*parser.Select, authorityStmt parser.Statement, execute bool) error {
	s.QueryLockAdmission = admission
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
			s.QueryLockAdmission.TableQueries[table] = analyze.PreparedSource{Query: queries[i], Ref: prior.Ref}
		}
		if err := s.acquireRelationTarget(selection, authorityStmt, execute, true); err != nil {
			return analyze.FutureCTEError(err, admission.FutureCTE[table])
		}
		leaf := &s.QuerySources[len(s.QuerySources)-1]
		// Keep SQL marks in the admission manifest for exact binder validation.
		// pg_sequences drops only effective physical marks during source capture.
		leaf.Mark, leaf.Query, leaf.Table, leaf.Only = mark, queries[i], table, table.Only
		entry := s.QueryLockAdmission.TableQueries[table]
		entry.Query, entry.Ref = queries[i], leaf.Ref
		s.QueryLockAdmission.TableQueries[table] = entry
	}
	return nil
}

// CurrentQuerySourceLocked validates an admitted leaf against catalogView and
// the storage journal changes sees (view.CurrentQuerySource).
func CurrentQuerySourceLocked(e *Engine, catalogView catalog.Read, changes *session.Journal, source exec.Leaf) (*catalog.Relation, error) {
	return view.CurrentQuerySource(catalogView, e.Catalog.Domain, e.Catalog.State.Epoch(), e.PrivateStorageLocked(changes, source.Ref.Address), source)
}

func (s *StatementScope) ValidateQuerySources(leaves []exec.Leaf) error {
	if len(s.mutations) != 0 && len(leaves) != len(s.QuerySources) {
		for _, grant := range s.QuerySources {
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
		e := s.Engine()
		catalog := e.PrivateCatalogLocked(s.PendingCatalog())
		for _, leaf := range leaves {
			if _, err := CurrentQuerySourceLocked(e, catalog, s.PendingCatalog(), leaf); err != nil {
				return err
			}
		}
		s.QuerySources = leaves
		return nil
	}
	if len(leaves) != len(s.QuerySources) {
		return ids.ErrCatalogReplaced
	}
	for i, leaf := range leaves {
		grant := s.QuerySources[i]
		if leaf.Ref != grant.Ref || leaf.Relation != grant.Relation || leaf.Storage != grant.Storage || leaf.Mark != grant.Mark || leaf.Only != grant.Only {
			return ids.ErrCatalogReplaced
		}
	}
	if len(s.mutations) != 0 {
		s.QuerySources = leaves
	}
	return nil
}

// Called once with e.mu held: all table roots share this statement frontier.
func (e *Engine) CaptureQuerySourcesLocked(scope *StatementScope, leaves []exec.Leaf, singleton bool) (*exec.Source, error) {
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: HostOf(scope)}
	}
	if command := scope.rlsCommand; command != nil && command.sources != nil {
		prepared := command.sources
		if len(prepared.leaves) != len(leaves) {
			return nil, ids.ErrCatalogReplaced
		}
		for i, leaf := range leaves {
			prior := prepared.leaves[i]
			if prior.Ref != leaf.Ref || prior.Relation != leaf.Relation || prior.Storage != leaf.Storage || prior.Mark != leaf.Mark || prior.Only != leaf.Only || prior.Principal != leaf.Principal || prior.AnalysisOnly != leaf.AnalysisOnly {
				return nil, ids.ErrCatalogReplaced
			}
		}
		prepared.singleton = singleton
		if err := e.materializePolicySourcesLocked(scope); err != nil {
			return nil, err
		}
		source, err := e.capturePreparedQuerySourcesLocked(scope, prepared, scope.QueryLockContext)
		if err == nil {
			err = e.fenceRowSecurityProgramsLocked(scope)
		}
		if err != nil && source != nil {
			e.closeQuerySourceSetLocked(scope.Op.Context(), source, nil)
			source = nil
		}
		return source, err
	}
	var invoker authorization.PrincipalRef
	for _, leaf := range leaves {
		if leaf.Relation.RelationKind == catalog.ViewRelation {
			auth, err := scope.routineAuthorizationLocked()
			if err != nil {
				return nil, err
			}
			invoker = auth.State.CurrentUser
			break
		}
	}
	return e.CaptureQuerySourcesForInvokerLocked(scope, leaves, singleton, invoker, plan.RowMark{}, scope.QueryLockContext)
}

// commentEngine returns the engine whose catalog the comment snapshot was
// captured from, or nil before capture.
func (s *StatementScope) commentEngine() *Engine {
	if s.Inquiry.CommentCatalog.Owner() == nil {
		return nil
	}
	return s.Engine()
}

// CaptureCommentSnapshotLocked shares one immutable catalog/role pair across
// query providers and completed-row CHECK evaluation. The caller holds e.mu.
func (e *Engine) CaptureCommentSnapshotLocked(scope *StatementScope) error {
	if scope.Inquiry.CommentCatalog.State() == nil {
		scope.Inquiry.CommentCatalog = e.PrivateCatalogLocked(scope.PendingCatalog())
		scope.Inquiry.commentRoles = e.RoleStore.Read(&e.Catalog.Owner, scope.Inquiry.CommentCatalog, e.OperationRolesLocked(scope.Op))
	}
	scope.Output.ACLItem = scope
	scope.Output.Regrole = scope
	scope.Output.Operator = scope
	scope.Output.Regcollation, scope.Output.Regnamespace = scope, scope
	scope.Output.RegTS = scope
	if scope.Inquiry.DeparsePath == nil {
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			return err
		}
		auth.Recapture(scope.Inquiry.CommentCatalog)
		scope.Inquiry.commentRoles = auth.Roles
		scope.Inquiry.deparseAuth = auth
		scope.Inquiry.DeparsePath, err = ReferencePath(auth)
		if err != nil {
			return err
		}
		scope.Inquiry.collationPath, err = LookupPath(auth, analyze.CollationLookup)
		if err != nil {
			return err
		}
	}
	scope.Input.Regrole = regobj.RoleInput{Roles: scope.Inquiry.commentRoles}
	input := captureRegObjectsOnPath(scope.Inquiry.deparseAuth, scope.Inquiry.collationPath)
	scope.Input.Regcollation, scope.Input.Regnamespace = input, input
	return nil
}

func (e *Engine) CaptureCheckCommentSnapshotLocked(scope *StatementScope, relation *catalog.Relation) error {
	for _, column := range relation.Columns {
		if column.Usage.Type.ContainsRegclass() || column.Usage.Type.ContainsOperatorAlias() || column.Usage.Type.ContainsACLItem() || column.Usage.Type.ContainsRegObject() || column.Usage.Type.ContainsRegTS() {
			if err := e.CaptureCommentSnapshotLocked(scope); err != nil {
				return err
			}
			scope.Output.Regclass = scope
			return nil
		}
	}
	for _, definition := range relation.ColumnMeta.Defaults {
		if definition.CatalogSnapshot {
			return e.CaptureCommentSnapshotLocked(scope)
		}
	}
	for _, definition := range relation.ColumnMeta.Generations {
		if definition.CatalogSnapshot {
			return e.CaptureCommentSnapshotLocked(scope)
		}
	}
	for _, check := range relation.Constraints.Check {
		if check.CommentSnapshot {
			return e.CaptureCommentSnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) CaptureCheckVisibilitySnapshotLocked(scope *StatementScope, relation *catalog.Relation) error {
	for _, check := range relation.Constraints.Check {
		if check.VisibilitySnapshot {
			return e.captureVisibilitySnapshotLocked(scope)
		}
	}
	return nil
}

func (e *Engine) CaptureQuerySourcesForInvokerLocked(scope *StatementScope, leaves []exec.Leaf, singleton bool, invoker authorization.PrincipalRef, inherited plan.RowMark, locks *exec.LockContext) (result *exec.Source, err error) {
	scope.rlsAdmissionDepth++
	prepared, err := e.prepareQuerySourcesLocked(scope, leaves, singleton, invoker, inherited)
	err = e.endRowSecurityAdmissionLocked(scope, err)
	if err != nil {
		return nil, err
	}
	if err = e.materializePolicySourcesLocked(scope); err != nil {
		return nil, err
	}
	result, err = e.capturePreparedQuerySourcesLocked(scope, prepared, locks)
	if err == nil {
		err = e.fenceRowSecurityProgramsLocked(scope)
	}
	if err != nil && result != nil {
		e.closeQuerySourceSetLocked(scope.Op.Context(), result, nil)
		result = nil
	}
	return result, err
}

// Metadata, stored roots and occurrence programs are already admitted. This
// phase only pins the one complete visibility union and builds physical sources.
func (e *Engine) capturePreparedQuerySourcesLocked(scope *StatementScope, prepared *querySourcePreparation, locks *exec.LockContext) (*exec.Source, error) {
	if prepared.consumed {
		return nil, ids.ErrCatalogReplaced
	}
	prepared.consumed = true
	leaves, singleton, invoker, inherited := prepared.leaves, prepared.singleton, prepared.invoker, prepared.inherited
	if locks == nil {
		locks = scope.QueryLockContext
	}
	if err := e.captureStatementSnapshotLocked(scope); err != nil {
		return nil, err
	}
	if scope.TxRef != (ids.TxRef{}) {
		if _, err := e.VisibilitySnapshotLocked(scope.TxRef, false); err != nil {
			return nil, err
		}
	}
	if scope.Inquiry.visibilityDemand {
		if err := e.captureVisibilitySnapshotLocked(scope); err != nil {
			return nil, err
		}
	}
	if len(leaves) == 0 {
		source := &exec.Source{Singleton: singleton, Locks: locks}
		borrowQueryAuthority(source, scope)
		return source, nil
	}
	var catalogView *visibility.View
	var err error
	needsView := len(scope.rlsDataRefs) != 0
	if !needsView {
		for _, leaf := range leaves {
			if leaf.Relation.RelationKind == catalog.PartitionedRelation {
				needsView = true
				break
			}
		}
	}
	if needsView {
		catalogView, err = scope.AcquireDataViewsLocked(e, prepared.refs)
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
		program := prepared.occurrences[i].program
		denied := program.Denied()
		if denied {
			source, err = view.CaptureDeniedQuerySource(catalogView, leaf.Ref)
		} else {
			switch leaf.Relation.RelationKind {
			case catalog.SequenceRelation:
				e.markTemporaryAccessLocked(scope, leaf.Ref, temporaryOwnAccess)
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
					err = SQLError("0A000", "row locking partition hierarchies is unsupported")
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
					source, err = partition.CaptureQueryPrefix(partition.CaptureCatalog(scope.Inquiry.CommentCatalog, scope.PartitionRelations), catalogView, leaf.Ref, leaf.Relation)
				}
			case catalog.TableRelation:
				source, err = table.CaptureInheritanceQueryPrefix(scope.inheritanceRead(scope.Inquiry.CommentCatalog), catalogView, leaf, scope.Op.Session.SessionToken)
				if err == nil && program == nil {
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
			case catalog.ViewRelation:
				admitted := prepared.occurrences[i].view
				if admitted == nil {
					err = ids.ErrCatalogReplaced
					break
				}
				var inputs *exec.Source
				inputs, err = e.captureStoredSourceLocked(scope, admitted, locks)
				if err == nil {
					source = &exec.Source{Ref: leaf.Ref, Authority: scope.authority, Locks: locks, NestedRoot: admitted.root, NestedInputs: inputs, Materialized: true, FlatWidth: len(admitted.root.Schema()), Inherited: mark}
					if locks != nil {
						occurrences := exec.QueryTreeOccurrences(admitted.root, inputs, nil)
						source.Lineage = &exec.QueryLockLineage{Root: admitted.root, Inputs: inputs, Stage: &exec.Source{Materialized: true, Occurrences: occurrences}}
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
				if scope.ruleAction.Auth != nil && leaf.Principal != (authorization.PrincipalRef{}) {
					sourceInvoker = leaf.Principal
				}
				source, err = e.openRelationSourceLocked(scope, leaf.Ref, sourceInvoker, mark, locks, uses)
				e.Mu.Lock()
			}
		}
		if err != nil {
			e.closeQuerySourceSetLocked(scope.Op.Context(), first, sources)
			return nil, err
		}
		if !denied && (leaf.Relation.RelationKind == catalog.TableRelation || leaf.Relation.RelationKind == catalog.PartitionedRelation) {
			e.initializeTemporarySourceLocked(scope, source)
		}
		if program == nil && !denied && (leaf.Relation.RelationKind == catalog.TableRelation || leaf.Relation.RelationKind == catalog.PartitionedRelation) {
			partition.CapturePruner(partition.CaptureCatalog(scope.inheritanceRead(scope.Inquiry.CommentCatalog), scope.PartitionRelations), leaf.Relation, source)
		}
		source.RLS = program
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
	e.Mu.Unlock()
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
	e.Mu.Lock()
}

// inquirySnapshot is the statement's catalog-inquiry snapshot: comment, deparse,
// visibility and namespace reads captured once per statement. It is a value
// group, not a pointer slot, so capturing it allocates nothing.
type inquirySnapshot struct {
	CommentCatalog         catalog.Read
	commentRoles           authorization.Read
	deparseAuth            AuthorizationView
	DeparsePath            []ids.ObjectAddress
	collationPath          []ids.ObjectAddress
	visibilityCatalog      catalog.Read
	visibilityRelationPath []ids.ObjectAddress
	visibilityOtherPath    []ids.ObjectAddress
	visibilityDemand       bool
	CurrentSchemaResult    sqltype.Value
	searchPathResults      [2]sqltype.Value
}

// captureHashIndexesLocked fills index.HashCapture from the published engine
// state. The caller holds e.mu, the same owner as visibility-prefix capture.
func (e *Engine) captureHashIndexesLocked(scope *StatementScope, leaf exec.Leaf, source *exec.Source, mark plan.RowMark) error {
	in := index.HashCapture{Eligible: scope.implicit && scope.Catalog == nil, Live: e.Catalog.State, Relations: e.Storage.Relations, Roots: e.Storage.IndexRoots, Commit: e.Visibility.CommitSeq()}
	return index.CaptureHashIndexesLocked(in, leaf, source, mark)
}

func borrowQueryAuthority(s *exec.Source, scope *StatementScope) {
	s.Authority = scope.authority
	if scope.sqlRoutine.binding != nil && s.RoutineRecipe == nil {
		// Borrow the same original frozen action manifest for runtime demand.
		// Nested stored views install their own consumer/manifest separately.
		s.RoutineChecks = scope.sqlRoutine.binding.Dependencies
	}
	s.Host = nil
	if s.Authority == nil {
		s.Host = HostOf(scope)
	}
}

func (e *Engine) captureSequenceSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation) (*exec.Source, error) {
	g := e.SequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return nil, fmt.Errorf("stuntgres: sequence state missing")
	}
	i := relation.Columns[0].Usage.Type
	last, _ := sqltype.Integer(i, g.Last)
	log, _ := sqltype.Integer(i, 0)
	return &exec.Source{Ref: ref, Anonymous: [][]sqltype.Value{{last, log, sqltype.Boolean(relation.Columns[2].Usage.Type, g.Called)}}}, nil
}
