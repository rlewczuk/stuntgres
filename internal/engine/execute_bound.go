package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type preparedCommandExecution struct {
	binding     analyze.Binding
	auth        AuthorizationView
	evaluation  exec.Context
	queryRoot   plan.Node
	expressions [][]expr.Expr
	primaryTag  string
}

// Enter with Engine.mu held; every outcome releases it. Binding, signature and
// parameter import are statement-wide and have already completed exactly once.
func (e *Engine) executeBoundCommandLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, canonical []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation, prepared *preparedCommandExecution) (Result, error) {
	op := scope.op
	ctx := op.Context()
	var err error
	expressions := b.Expressions
	expressionsChanged := false
	quoteCaptures := evaluation.QuoteCaptures
	_, inserting := stmt.(*parser.Insert)
	var queryRoot plan.Node
	_, deleting := stmt.(*parser.Delete)
	if (scope.protocolOrZero().command || scope.protocolOrZero().simple) && protocolPlainSelect(stmt) {
		// Plan closed immutable recipes before executor startup, retaining
		// imported-parameter, row, domain and volatile demand barriers. The
		// private folded graph is also the graph retained by live portals.
		evaluation.PlanStaticOperators = true
	}
	routineAuth := auth
	if scope.ruleAction.auth != nil {
		routineAuth.State.CurrentUser = scope.routineAuth.State.CurrentUser
		if err := routineAuth.Roles.ValidatePrincipal(routineAuth.State.CurrentUser); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.routineAuth = routineAuth
	scope.routineDependencies = b.Dependencies
	if inserting || b.Merge != nil || b.Update != nil || b.Query != nil || (deleting && (b.Delete.Using != nil || b.Predicate != nil || b.Returning != nil)) || len(b.Insert.Defaults) != 0 {
		// Planning-owned conversions precede executor ACLs, without retaining
		// Engine.mu or acquiring a data view. Analysis/namespace checks remain
		// earlier, and only this execution's imported frame is materialized.
		e.mu.Unlock()
		if err := scope.readmitPreparedPrograms(routineAuth); err != nil {
			return Result{}, err
		}
		if evaluation.PlanStaticOperators && deleting {
			b, err = view.RuleMutationProducerDemand(b)
			if err != nil {
				return Result{}, err
			}
		}
		if conflict := dml.NewInsertConflictState(b.Insert.Conflict); conflict != nil {
			if conflict.Predicate != nil {
				conflict.Predicate, err = dml.NormalizeConflictPredicate(&evaluation, conflict.Predicate)
				if err != nil {
					return Result{}, err
				}
			}
			if err := conflict.SelectArbiters(&evaluation, view.PhysicalBinding(b).Relation, auth.Catalog); err != nil {
				return Result{}, err
			}
			scope.InsertConflict = conflict
			if err := dml.PreflightConflictUpdate(conflict, &evaluation); err != nil {
				return Result{}, err
			}
		}
		if inserting {
			b.ArrayTargets, err = exec.PreflightArrayTargets(&evaluation, b.ArrayTargets)
			if err != nil {
				return Result{}, err
			}
			// Supplied values retain priority over defaults and executor ACLs.
			// Constant planning and frame conversions both run unlocked; only
			// changed rows and the outer slice need execution-private copies.
			destination, destinationTargets := b.Relation, b.Targets
			if b.ViewWrite != nil {
				destination, destinationTargets = view.ViewWriteOf(b.ViewWrite).Logical.Relation, view.ViewWriteOf(b.ViewWrite).Logical.Targets
			}
			for i, row := range b.Expressions {
				var privateRow []expr.Expr
				for j, expression := range row {
					if len(b.Expressions) == 1 && len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[b.Targets[j]] {
						continue
					}
					folded, changed, foldErr := exec.PreflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, analyze.InsertValueError(auth.Catalog, foldErr, destination.Columns[destinationTargets[j]], b.Insert.Rows[i][j], nil)
					}
					if changed {
						if privateRow == nil {
							privateRow = append([]expr.Expr(nil), row...)
						}
						privateRow[j] = folded
					}
				}
				if privateRow != nil {
					if !expressionsChanged {
						expressions = append([][]expr.Expr(nil), b.Expressions...)
						expressionsChanged = true
					}
					expressions[i] = privateRow
				}
			}
		}
		if b.Merge != nil {
			err = dml.PreflightMerge(&evaluation, b.Merge, auth.Catalog)
		} else if b.Update != nil {
			if evaluation.PlanStaticOperators {
				b.Update, err = dml.PreflightUpdate(&evaluation, b.Update, func(prepared *analyze.UpdateBinding) (plan.Node, error) {
					demandBinding := b
					demandBinding.Update = prepared
					demanded, err := view.RuleMutationProducerDemand(demandBinding)
					if err != nil {
						return nil, err
					}
					return demanded.Update.From, nil
				})
			} else {
				b.Update, err = dml.PreflightUpdate(&evaluation, b.Update)
			}
			if err == nil && b.Update.From != nil {
				err = view.ValidateQueryJoinExecution(&evaluation, b.Update.From, auth.Catalog, 0)
			}
		} else if b.Query != nil {
			queryRoot, err = view.PreflightQuery(&evaluation, b.Query.Root)
			if err == nil {
				err = view.ValidateQueryJoinExecution(&evaluation, queryRoot, auth.Catalog, 0)
			}
		} else if deleting {
			if b.Delete.Using != nil {
				b.Delete.Using, err = view.PreflightQuery(&evaluation, b.Delete.Using)
				if err == nil {
					err = view.ValidateQueryJoinExecution(&evaluation, b.Delete.Using, auth.Catalog, 0)
				}
			}
			if err == nil && b.Predicate != nil {
				b.Predicate, _, err = exec.PreflightCoercions(&evaluation, b.Predicate)
			}
		}
		if err == nil && b.Returning != nil {
			b.Returning, err = dml.PreflightReturning(&evaluation, b.Returning)
		}
		if err == nil && (inserting || b.Update != nil || deleting || b.Merge != nil) {
			err = view.ValidateMutationJoinExecution(&evaluation, &b, expressions, auth.Catalog)
		}
		if err != nil {
			return Result{}, err
		}
		if b.ViewWrite != nil {
			var checks []view.CheckGroup
			for i, group := range view.ViewWriteOf(b.ViewWrite).Checks {
				var predicates []expr.Expr
				for j, expression := range group.Predicates {
					folded, changed, foldErr := exec.PreflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, foldErr
					}
					if changed {
						if predicates == nil {
							predicates = append([]expr.Expr(nil), group.Predicates...)
						}
						predicates[j] = folded
					}
				}
				if predicates != nil {
					if checks == nil {
						checks = append([]view.CheckGroup(nil), view.ViewWriteOf(b.ViewWrite).Checks...)
					}
					checks[i].Predicates = predicates
				}
			}
			if checks != nil {
				view.ViewWriteOf(b.ViewWrite).Checks = checks
			}
		}
		// Both INSERT paths plan only defaults this binding actually consumes.
		// Demand immutable conversions even for an empty query source, while
		// keeping the stored definition and any runtime-only producer unfolded.
		var privateDefaults []expr.Expr
		for i, expression := range b.Insert.Defaults {
			if expression == nil {
				continue
			}
			defaultEvaluation := evaluation
			analyze.ApplyDefaultOrigin(view.BindingDefaultOrigin(&b, i), &defaultEvaluation)
			folded, changed, foldErr := exec.PreflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if foldErr != nil {
				return Result{}, foldErr
			}
			if err := view.ValidateExpressionJoinExecution(&defaultEvaluation, folded, auth.Catalog); err != nil {
				return Result{}, err
			}
			if changed {
				if privateDefaults == nil {
					privateDefaults = append([]expr.Expr(nil), b.Insert.Defaults...)
				}
				privateDefaults[i] = folded
			}
		}
		if privateDefaults != nil {
			b.Insert.Defaults = privateDefaults
		}
		e.mu.Lock()
		if err = ctx.Err(); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = scope.validateLockedRelation(e); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		// Do not authorize source capture with the pre-unlock authority view.
		// Keep the original binding's actual-column manifest and exact ref.
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.routineAuth = auth
		e.mu.Unlock()
		err = scope.readmitPreparedPrograms(scope.routineAuth)
		e.mu.Lock()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if scope.ruleAction.auth != nil {
			auth.State.CurrentUser = scope.ruleAction.auth.State.CurrentUser
			if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Preserve effect-free planning errors before catalog mutation admission,
	// using refreshed role authority before table/source access or row effects.
	if analyze.GuardedCatalogRelation(b.Relation) {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
			err := dml.GuardedCatalogMutationError(auth.View, b.Relation, false)
			e.mu.Unlock()
			return Result{}, err
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if b.ViewWrite == nil && b.Relation.RelationKind != catalog.MaterializedViewRelation && b.Relation.RelationKind != catalog.ForeignRelation {
			if err := analyze.TableKindError(b.Relation, "change"); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Released MERGE checks read-only after analysis/planning conversions, but
	// before executor column/source ACLs. Other statements keep their ordering.
	if b.Merge != nil && scope.owner.ReadOnly && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		tag := "MERGE"
		if prepared != nil && prepared.primaryTag != "" {
			tag = prepared.primaryTag
		}
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if err = checkRowSecurityBinding(scope, auth, b); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = analyze.CheckBinding(auth, b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil && b.Parameters.Count == 0 && quoteCaptures.Private {
		scope.Expected.quotes = quoteCaptures.Entries
		scope.Expected.quoteGeneration = op.Session.DiscardPlansGeneration
	}
	quoteCaptures.Sealed = true
	scope.quoteCaptures = quoteCaptures
	b = view.PhysicalBinding(b)
	if err = partition.WriteExtensionBoundary(stmt, b.Relation); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	tag := session.ModifyingTag(stmt)
	if prepared != nil && prepared.primaryTag != "" {
		tag = prepared.primaryTag
	}
	selection, selecting := stmt.(*parser.Select)
	if declaration, ok := stmt.(*parser.DeclareCursor); ok {
		selection, selecting = declaration.Query, true
		tag = session.ModifyingTag(selection)
	}
	// PostgreSQL reports the rewritten SELECT command for a marked view,
	// including constant views without physical lock leaves. Physical table
	// rowmarks retain their original SELECT FOR ... diagnostic.
	if scope.owner.ReadOnly {
		if selecting {
			for _, leaf := range b.QuerySources {
				if leaf.Mark.Mode != 0 && leaf.Relation.RelationKind == catalog.ViewRelation {
					tag = "SELECT"
					break
				}
			}
		}
	}
	if selecting && tag == "" && scope.owner.ReadOnly && b.Query != nil {
		mode, modeErr := e.firstQueryRowModeLocked(scope, b.Query.Root, b.QuerySources)
		if modeErr != nil {
			e.mu.Unlock()
			return Result{}, modeErr
		}
		if mode != 0 {
			tag = "SELECT"
		}
	}
	if scope.owner.ReadOnly && tag != "" && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if b.Relation != nil && b.Relation.RelationKind == catalog.MaterializedViewRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			e.mu.Unlock()
			return Result{}, analyze.TableKindError(b.Relation, "change")
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		if err := access.CheckTemporaryStorageAccess(auth.Catalog, b.Relation, op.Session.SessionToken); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if b.Query != nil || b.SubqueryDemand || len(b.QuerySources) != 0 {
		if err = e.initializeQueryLocksLocked(scope, queryRoot, b.QuerySources); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.VisibilitySnapshot
	if b.Query != nil {
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.Query.VisibilitySnapshot
	}
	if b.Returning != nil {
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.Returning.VisibilitySnapshot
	}
	if scope.inquiry.visibilityDemand {
		err = e.captureVisibilitySnapshotLocked(scope)
	}
	if err == nil && b.Relation != nil {
		err = e.captureCheckVisibilitySnapshotLocked(scope, b.Relation)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.Prepared.RegclassConcat && b.Query == nil {
		if err = e.captureCommentSnapshotLocked(scope); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if b.Relation != nil && b.Relation.RelationKind == catalog.ForeignRelation {
		operation, modifying := catalog.ForeignInsert, true
		switch stmt.(type) {
		case *parser.Insert:
			// Binding has already applied Restore's captured namespace and
			// authenticated reconstruction authority. Reject its actual
			// foreign target before capability admission or provider capture,
			// without a second lookup or changes to ordinary diagnostics.
			if operationReplayOrZero(op).insertAuthority {
				e.mu.Unlock()
				return Result{}, sqlError("0A000", "restore cannot write foreign tables")
			}
			if b.Insert.Conflict != nil {
				e.mu.Unlock()
				return Result{}, sqlError("0A000", "ON CONFLICT on foreign tables is unsupported in embedded mode")
			}
		case *parser.Update:
			operation = catalog.ForeignUpdate
		case *parser.Delete:
			operation = catalog.ForeignDelete
		default:
			modifying = false
		}
		if modifying {
			if err := fdw.ModifyAdmission(auth.Catalog, b.Relation, operation); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	if err := e.configureRowSecurityLocked(scope, b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	// Static operator planning belongs to preparation, never to a later row,
	// derived provider, or runtime conditional invocation.
	evaluation.PlanStaticOperators = false
	evaluation.SpecializeRuleParameters = false
	execution := preparedCommandExecution{binding: b, auth: auth, evaluation: evaluation, queryRoot: queryRoot, expressions: expressions}
	if prepared != nil {
		execution.primaryTag = prepared.primaryTag
		*prepared = execution
		e.mu.Unlock()
		return Result{}, nil
	}
	return e.executePreparedCommandLocked(scope, stmt, &execution, canonical, creationStage, creationDescriptor)
}

func (e *Engine) executePreparedCommandLocked(scope *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, canonical []sqltype.Value, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (Result, error) {
	op := scope.op
	ctx := op.Context()
	var err error
	b, auth := execution.binding, execution.auth
	queryRoot := execution.queryRoot
	if scope.protocolOrZero().simple && !scope.protocolOrZero().simpleCreated {
		e.mu.Unlock()
		if err := scope.beginProtocolSimplePortal(); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
	}
	if b.Relation != nil && b.Relation.RelationKind == catalog.ForeignRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			return e.executeForeignCommandLocked(scope, stmt, execution, canonical)
		}
	}
	switch s := stmt.(type) {
	case *parser.Merge:
		err := e.captureCheckCommentSnapshotLocked(scope, b.Relation)
		if err == nil && b.Returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		collectAuth := auth
		collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
		refs, err := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, []ids.ObjectRef{b.Ref}, scope.op.Session.SessionToken, plan.RowMark{})
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		dataView, err := scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var target *exec.Source
		if scope.RLS.TargetDenied {
			target, err = view.CaptureDeniedQuerySource(dataView, b.Ref)
		} else {
			target, err = exec.CaptureSource(ctx, dataView, b.Ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		inputs, err := e.captureQuerySourcesLocked(scope, b.QuerySources, false)
		if err != nil {
			target.Close()
			e.mu.Unlock()
			return Result{}, err
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.mu.Unlock()
		return executeMerge(ctx, scope, b, target, inputs, canonical, schema)
	case *parser.Update:
		err = e.captureCheckCommentSnapshotLocked(scope, b.Relation)
		for _, target := range scope.InheritedTargets {
			if err != nil {
				break
			}
			err = e.captureCheckCommentSnapshotLocked(scope, target.Relation)
		}
		if err == nil && b.Returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var view *visibility.View
		if len(b.QuerySources) != 0 || len(scope.InheritedTargets) != 0 {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, collectErr := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, scope.Command.TargetRefs(b.Ref), scope.op.Session.SessionToken, plan.RowMark{})
			if collectErr != nil {
				e.mu.Unlock()
				return Result{}, collectErr
			}
			view, err = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, err = scope.acquireDataViewLocked(e, b.Ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		source, err := scope.Command.CaptureSource(ctx, view, b)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var from *exec.Source
		if b.Update.From != nil || b.SubqueryDemand {
			from, err = e.captureQuerySourcesLocked(scope, b.QuerySources, false)
			if err != nil {
				source.Close()
				e.mu.Unlock()
				return Result{}, err
			}
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.mu.Unlock()
		return executeUpdate(ctx, scope, b, source, from, canonical, schema)
	case *parser.Insert:
		return e.executeInsertLocked(scope, execution, canonical)
	case *parser.Delete:
		var view *visibility.View
		var viewErr error
		if len(b.QuerySources) != 0 || len(scope.InheritedTargets) != 0 {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, err := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, scope.Command.TargetRefs(b.Ref), scope.op.Session.SessionToken, plan.RowMark{})
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			view, viewErr = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, viewErr = scope.acquireDataViewLocked(e, b.Ref)
		}
		var inputs *exec.Source
		if viewErr == nil && (b.Delete.Using != nil || b.SubqueryDemand) {
			inputs, viewErr = e.captureQuerySourcesLocked(scope, b.QuerySources, false)
		} else if viewErr == nil && b.Returning != nil {
			viewErr = e.captureCommentSnapshotLocked(scope)
		}
		e.mu.Unlock()
		if viewErr != nil {
			view.Release()
			return Result{}, viewErr
		}
		return executeDelete(scope, b, view, inputs, canonical)
	case *parser.CreateQueryTable:
		return e.executeQueryTableLocked(scope, s, b, queryRoot, canonical, creationStage, creationDescriptor)
	case *parser.DeclareCursor:
		// Even an unlocked cursor retains physical lineage through derived/view
		// projections. Zero-mode entries capture identity, never row grants.
		if scope.queryLocks == nil {
			scope.queryLocks = &exec.LockContext{IdentityOnly: true}
		}
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.QuerySources, s.Query.From == nil && s.Query.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			source.Close()
			return Result{}, err
		}
		return scope.installSQLCursor(s, queryRoot, source, canonical)
	case *parser.Select:
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.QuerySources, s.From == nil && s.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		if b.Rule.Template {
			source = view.FreezeWriteSources(source, b.QuerySources, scope.authority, hostOf(scope))
		}
		if scope.protocolOrZero().bind != nil {
			return scope.installProtocolProducer(queryRoot, source, canonical)
		}
		if scope.sqlRoutine.scalar {
			rows, count, err := finalizeSQLRoutineQuery(ctx, queryRoot, source, canonical)
			return Result{Count: int64(count), Rows: rows}, err
		}
		rows, count, queryErr := exec.FinalizeSelect(ctx, queryRoot, source, canonical, op.ParallelWorkers)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if b.Query.RegclassOutput && count != 0 {
			if err := e.captureRegclassOutput(op, rows.(*exec.FinalRows)); err != nil {
				rows.Close()
				return Result{}, err
			}
		}
		return Result{Count: int64(count), Rows: rows}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
}
