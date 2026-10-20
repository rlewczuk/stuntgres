package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type preparedCommandExecution struct {
	binding     binding
	auth        AuthorizationView
	evaluation  EvalContext
	queryRoot   plan.Node
	expressions [][]expr.Expr
	primaryTag  string
}

// Enter with Engine.mu held; every outcome releases it. Binding, signature and
// parameter import are statement-wide and have already completed exactly once.
func (e *Engine) executeBoundCommandLocked(scope *StatementScope, stmt parser.Statement, b binding, auth AuthorizationView, canonical []sqltype.Value, evaluation EvalContext, creationStage *CatalogStage, creationDescriptor *catalog.Relation, prepared *preparedCommandExecution) (Result, error) {
	op := scope.op
	ctx := op.Context()
	var err error
	expressions := b.expressions
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
	if scope.ruleActionAuth != nil {
		routineAuth.State.CurrentUser = scope.routineAuth.State.CurrentUser
		if err := routineAuth.Roles.ValidatePrincipal(routineAuth.State.CurrentUser); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.routineAuth = routineAuth
	scope.routineDependencies = b.dependencies
	if inserting || b.merge != nil || b.update != nil || b.query != nil || (deleting && (b.delete.using != nil || b.predicate != nil || b.returning != nil)) || len(b.insert.defaults) != 0 {
		// Planning-owned conversions precede executor ACLs, without retaining
		// Engine.mu or acquiring a data view. Analysis/namespace checks remain
		// earlier, and only this execution's imported frame is materialized.
		e.mu.Unlock()
		if err := scope.readmitPreparedPrograms(routineAuth); err != nil {
			return Result{}, err
		}
		if evaluation.PlanStaticOperators && deleting {
			b, err = ruleMutationProducerDemand(b)
			if err != nil {
				return Result{}, err
			}
		}
		if b.insert.conflict != nil {
			if b.insert.conflict.predicate != nil {
				b.insert.conflict.predicate, err = normalizeConflictPredicate(&evaluation, b.insert.conflict.predicate)
				if err != nil {
					return Result{}, err
				}
			}
			if err := b.insert.conflict.selectArbiters(&evaluation, b.physicalBinding().relation, auth.Catalog); err != nil {
				return Result{}, err
			}
			scope.insertConflict = b.insert.conflict
			if err := b.insert.conflict.preflightUpdate(&evaluation); err != nil {
				return Result{}, err
			}
		}
		if inserting {
			b.arrayTargets, err = preflightArrayTargets(&evaluation, b.arrayTargets)
			if err != nil {
				return Result{}, err
			}
			// Supplied values retain priority over defaults and executor ACLs.
			// Constant planning and frame conversions both run unlocked; only
			// changed rows and the outer slice need execution-private copies.
			destination, destinationTargets := b.relation, b.targets
			if b.viewWrite != nil {
				destination, destinationTargets = b.viewWrite.logical.relation, b.viewWrite.logical.targets
			}
			for i, row := range b.expressions {
				var privateRow []expr.Expr
				for j, expression := range row {
					if len(b.expressions) == 1 && len(b.insert.identityDiscard) != 0 && b.insert.identityDiscard[b.targets[j]] {
						continue
					}
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, insertValueError(auth.Catalog, foldErr, destination.Columns[destinationTargets[j]], b.insert.rows[i][j], nil)
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
						expressions = append([][]expr.Expr(nil), b.expressions...)
						expressionsChanged = true
					}
					expressions[i] = privateRow
				}
			}
		}
		if b.merge != nil {
			err = preflightMerge(&evaluation, b.merge, auth.Catalog)
		} else if b.update != nil {
			if evaluation.PlanStaticOperators {
				b.update, err = preflightUpdate(&evaluation, b.update, func(prepared *boundUpdate) (plan.Node, error) {
					demandBinding := b
					demandBinding.update = prepared
					demanded, err := ruleMutationProducerDemand(demandBinding)
					if err != nil {
						return nil, err
					}
					return demanded.update.from, nil
				})
			} else {
				b.update, err = preflightUpdate(&evaluation, b.update)
			}
			if err == nil && b.update.from != nil {
				err = validateQueryJoinExecution(&evaluation, b.update.from, auth.Catalog, 0)
			}
		} else if b.query != nil {
			queryRoot, err = preflightQuery(&evaluation, b.query.Root)
			if err == nil {
				err = validateQueryJoinExecution(&evaluation, queryRoot, auth.Catalog, 0)
			}
		} else if deleting {
			if b.delete.using != nil {
				b.delete.using, err = preflightQuery(&evaluation, b.delete.using)
				if err == nil {
					err = validateQueryJoinExecution(&evaluation, b.delete.using, auth.Catalog, 0)
				}
			}
			if err == nil && b.predicate != nil {
				b.predicate, _, err = preflightCoercions(&evaluation, b.predicate)
			}
		}
		if err == nil && b.returning != nil {
			b.returning, err = preflightReturning(&evaluation, b.returning)
		}
		if err == nil && (inserting || b.update != nil || deleting || b.merge != nil) {
			err = validateMutationJoinExecution(&evaluation, &b, expressions, auth.Catalog)
		}
		if err != nil {
			return Result{}, err
		}
		if b.viewWrite != nil {
			var checks []viewCheckGroup
			for i, group := range b.viewWrite.checks {
				var predicates []expr.Expr
				for j, expression := range group.predicates {
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, foldErr
					}
					if changed {
						if predicates == nil {
							predicates = append([]expr.Expr(nil), group.predicates...)
						}
						predicates[j] = folded
					}
				}
				if predicates != nil {
					if checks == nil {
						checks = append([]viewCheckGroup(nil), b.viewWrite.checks...)
					}
					checks[i].predicates = predicates
				}
			}
			if checks != nil {
				b.viewWrite.checks = checks
			}
		}
		// Both INSERT paths plan only defaults this binding actually consumes.
		// Demand immutable conversions even for an empty query source, while
		// keeping the stored definition and any runtime-only producer unfolded.
		var privateDefaults []expr.Expr
		for i, expression := range b.insert.defaults {
			if expression == nil {
				continue
			}
			defaultEvaluation := evaluation
			b.defaultOriginAt(i).apply(&defaultEvaluation)
			folded, changed, foldErr := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if foldErr != nil {
				return Result{}, foldErr
			}
			if err := validateExpressionJoinExecution(&defaultEvaluation, folded, auth.Catalog); err != nil {
				return Result{}, err
			}
			if changed {
				if privateDefaults == nil {
					privateDefaults = append([]expr.Expr(nil), b.insert.defaults...)
				}
				privateDefaults[i] = folded
			}
		}
		if privateDefaults != nil {
			b.insert.defaults = privateDefaults
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
		if scope.ruleActionAuth != nil {
			auth.State.CurrentUser = scope.ruleActionAuth.State.CurrentUser
			if err = auth.Roles.ValidatePrincipal(auth.State.CurrentUser); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Preserve effect-free planning errors before catalog mutation admission,
	// using refreshed role authority before table/source access or row effects.
	if guardedCatalogRelation(b.relation) {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
			err := guardedCatalogMutationError(auth, b.relation, false)
			e.mu.Unlock()
			return Result{}, err
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if b.viewWrite == nil && b.relation.RelationKind != catalog.MaterializedViewRelation && b.relation.RelationKind != catalog.ForeignRelation {
			if err := tableKind(b.relation, "change"); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Released MERGE checks read-only after analysis/planning conversions, but
	// before executor column/source ACLs. Other statements keep their ordering.
	if b.merge != nil && scope.owner.readOnly && !temporaryDataTarget(auth, &b, stmt) {
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
	if err = auth.checkBinding(b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil && b.parameters.Count == 0 && quoteCaptures.Private {
		scope.Expected.quotes = quoteCaptures.Entries
		scope.Expected.quoteGeneration = op.session.discardPlansGeneration
	}
	quoteCaptures.Sealed = true
	scope.quoteCaptures = quoteCaptures
	b = b.physicalBinding()
	if err = partitionWriteExtensionBoundary(stmt, b.relation); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	tag := modifyingTag(stmt)
	if prepared != nil && prepared.primaryTag != "" {
		tag = prepared.primaryTag
	}
	selection, selecting := stmt.(*parser.Select)
	if declaration, ok := stmt.(*parser.DeclareCursor); ok {
		selection, selecting = declaration.Query, true
		tag = modifyingTag(selection)
	}
	// PostgreSQL reports the rewritten SELECT command for a marked view,
	// including constant views without physical lock leaves. Physical table
	// rowmarks retain their original SELECT FOR ... diagnostic.
	if scope.owner.readOnly {
		if selecting {
			for _, leaf := range b.querySources {
				if leaf.Mark.Mode != 0 && leaf.Relation.RelationKind == catalog.ViewRelation {
					tag = "SELECT"
					break
				}
			}
		}
	}
	if selecting && tag == "" && scope.owner.readOnly && b.query != nil {
		mode, modeErr := e.firstQueryRowModeLocked(scope, b.query.Root, b.querySources)
		if modeErr != nil {
			e.mu.Unlock()
			return Result{}, modeErr
		}
		if mode != 0 {
			tag = "SELECT"
		}
	}
	if scope.owner.readOnly && tag != "" && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if b.relation != nil && b.relation.RelationKind == catalog.MaterializedViewRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			e.mu.Unlock()
			return Result{}, tableKind(b.relation, "change")
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		if err := checkTemporaryStorageAccess(auth.Catalog, b.relation, op.session.token); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if b.query != nil || b.subqueryDemand || len(b.querySources) != 0 {
		if err = e.initializeQueryLocksLocked(scope, queryRoot, b.querySources); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.visibilitySnapshot
	if b.query != nil {
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.query.VisibilitySnapshot
	}
	if b.returning != nil {
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || b.returning.visibilitySnapshot
	}
	if scope.inquiry.visibilityDemand {
		err = e.captureVisibilitySnapshotLocked(scope)
	}
	if err == nil && b.relation != nil {
		err = e.captureCheckVisibilitySnapshotLocked(scope, b.relation)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.prepared.regclassConcat && b.query == nil {
		if err = e.captureCommentSnapshotLocked(scope); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if b.relation != nil && b.relation.RelationKind == catalog.ForeignRelation {
		operation, modifying := catalog.ForeignInsert, true
		switch stmt.(type) {
		case *parser.Insert:
			if b.insert.conflict != nil {
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
			if err := foreignModifyAdmission(auth.Catalog, b.relation, operation); err != nil {
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
	b, auth, evaluation := execution.binding, execution.auth, execution.evaluation
	queryRoot, expressions := execution.queryRoot, execution.expressions
	if scope.protocolOrZero().simple && !scope.protocolOrZero().simpleCreated {
		e.mu.Unlock()
		if err := scope.beginProtocolSimplePortal(); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
	}
	if b.relation != nil && b.relation.RelationKind == catalog.ForeignRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			return e.executeForeignCommandLocked(scope, stmt, execution, canonical)
		}
	}
	switch s := stmt.(type) {
	case *parser.Merge:
		err := e.captureCheckCommentSnapshotLocked(scope, b.relation)
		if err == nil && b.returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		collectAuth := auth
		collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
		refs, err := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.querySources, []ids.ObjectRef{b.ref}, scope.op.session.token, plan.RowMark{})
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		view, err := scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var target *querySource
		if scope.rlsTargetDenied {
			target, err = captureDeniedQuerySource(view, b.ref)
		} else {
			target, err = captureQuerySource(ctx, view, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		inputs, err := e.captureQuerySourcesLocked(scope, b.querySources, false)
		if err != nil {
			target.Close()
			e.mu.Unlock()
			return Result{}, err
		}
		schema := auth.Catalog.Get(b.relation.Namespace()).Name()
		e.mu.Unlock()
		return e.executeMerge(ctx, scope, b, target, inputs, canonical, schema)
	case *parser.Update:
		err = e.captureCheckCommentSnapshotLocked(scope, b.relation)
		for _, target := range scope.inheritedTargets {
			if err != nil {
				break
			}
			err = e.captureCheckCommentSnapshotLocked(scope, target.Relation)
		}
		if err == nil && b.returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var view *visibility.View
		if len(b.querySources) != 0 || len(scope.inheritedTargets) != 0 {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, collectErr := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.querySources, scope.mutationTargetRefs(b.ref), scope.op.session.token, plan.RowMark{})
			if collectErr != nil {
				e.mu.Unlock()
				return Result{}, collectErr
			}
			view, err = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, err = scope.acquireDataViewLocked(e, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		source, err := scope.captureMutationSource(ctx, view, b)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var from *querySource
		if b.update.from != nil || b.subqueryDemand {
			from, err = e.captureQuerySourcesLocked(scope, b.querySources, false)
			if err != nil {
				source.Close()
				e.mu.Unlock()
				return Result{}, err
			}
		}
		schema := auth.Catalog.Get(b.relation.Namespace()).Name()
		e.mu.Unlock()
		return e.executeUpdate(ctx, scope, b, source, from, canonical, schema)
	case *parser.Insert:
		if err := e.captureCheckCommentSnapshotLocked(scope, b.relation); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err := scope.capturePartitionInsertLocked(e, auth, b.relation); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.beginInsertReturning(b)
		keepReturning := false
		if scope.insertReturning != nil {
			defer func() {
				if !keepReturning {
					scope.insertReturning.rows.Close()
				}
			}()
		}
		if b.commandQuery {
			source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, len(b.querySources) == 0)
			if sourceErr != nil {
				e.mu.Unlock()
				return Result{}, sourceErr
			}
			schema := auth.Catalog.Get(b.relation.Namespace()).Name()
			e.mu.Unlock()
			if err := exec.SelectQuerySources(ctx, source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil || b.rule.template {
				source = freezeViewWriteSources(scope, source, b.querySources)
			}
			b.query = &plan.Query{Root: queryRoot}
			rows, queryErr := evaluateInsertQuery(ctx, b, source, canonical, schema, scope.input, scope.output, scope)
			if queryErr != nil {
				return Result{}, queryErr
			}
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if scope.insertReturning != nil {
				result, err := scope.finishInsertReturning()
				keepReturning = err == nil
				return result, err
			}
			count, err := scope.applyInsertValidated(b.ref, rows)
			if err != nil {
				return Result{}, err
			}
			return Result{Count: int64(count)}, nil
		}
		if b.subqueryDemand {
			inputs, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, false)
			if sourceErr != nil {
				e.mu.Unlock()
				return Result{}, sourceErr
			}
			state := &queryState{Source: inputs, Parameters: canonical, Input: scope.input, Output: scope.output, Locking: inputs.Locks != nil, Ctes: scope.cteInvocation}
			defer state.ReleaseMemberships()
			evaluation.Query = state
			evaluation.Scope, evaluation.Frame = 1, 1
		}
		e.mu.Unlock()
		if evaluation.Query != nil {
			if err := exec.SelectQuerySources(ctx, evaluation.Query.Source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil || b.rule.template {
				evaluation.Query.Source = freezeViewWriteSources(scope, evaluation.Query.Source, b.querySources)
			}
			defer evaluation.Query.Source.Close()
		}
		var rows indexWriteBatch
		if scope.insertReturning == nil {
			rows.values = make([][]sqltype.Value, 0, len(b.insert.rows))
		}
		for i, source := range b.insert.rows {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			row := make([]sqltype.Value, len(b.relation.Columns))
			for j, c := range b.relation.Columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			if len(b.insert.projection) != 0 {
				if err = evaluateInsertValuesRow(auth.Catalog, &b, expressions[i], source, row, len(b.insert.rows) > 1, &evaluation); err != nil {
					return Result{}, err
				}
				schema := auth.Catalog.Get(b.relation.Namespace()).Name()
				if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
					return Result{}, err
				}
				candidate := indexWriteCandidate{values: row}
				admitted, admitErr := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
				if admitErr != nil {
					return Result{}, admitErr
				}
				if admitted {
					if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
						return Result{}, err
					}
					rows.append(candidate)
				}
				continue
			}
			if len(b.insert.defaults) != 0 {
				supplied := b.expressions[i]
				for j, target := range b.targets {
					if j < len(supplied) && supplied[j] != nil && (len(b.arrayTargets) == 0 || b.arrayTargets[j] == nil) {
						row[target] = sqltype.Value{}
					}
				}
				for j, expression := range b.insert.defaults {
					if expression != nil && row[j].Type() != nil {
						row[j], err = b.evalDefault(&evaluation, j, expression)
						if err != nil {
							return Result{}, err
						}
					}
				}
			}
			for j, v := range source {
				if b.expressions[i][j] == nil {
					continue
				}
				target := b.targets[j]
				c := b.relation.Columns[target]
				var value sqltype.Value
				if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
					value, err = b.arrayTargets[j].ApplyExpression(&evaluation, row[target], expressions[i][j])
				} else {
					value, err = expressions[i][j].Eval(&evaluation)
				}
				if err == nil {
					row[target] = value
				}
				if err != nil {
					return Result{}, insertValueError(auth.Catalog, err, c, v, nil)
				}
			}
			schema := auth.Catalog.Get(b.relation.Namespace()).Name()
			if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return Result{}, err
			}
			candidate := indexWriteCandidate{values: row}
			admitted, admitErr := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
			if admitErr != nil {
				return Result{}, admitErr
			}
			if admitted {
				if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
					return Result{}, err
				}
				rows.append(candidate)
			}
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if scope.insertReturning != nil {
			result, err := scope.finishInsertReturning()
			keepReturning = err == nil
			return result, err
		}
		count, err := scope.applyInsertValidated(b.ref, rows)
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(count)}, nil
	case *parser.Delete:
		var view *visibility.View
		var viewErr error
		if len(b.querySources) != 0 || len(scope.inheritedTargets) != 0 {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, err := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.querySources, scope.mutationTargetRefs(b.ref), scope.op.session.token, plan.RowMark{})
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			view, viewErr = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, viewErr = scope.acquireDataViewLocked(e, b.ref)
		}
		var inputs *querySource
		if viewErr == nil && (b.delete.using != nil || b.subqueryDemand) {
			inputs, viewErr = e.captureQuerySourcesLocked(scope, b.querySources, false)
		} else if viewErr == nil && b.returning != nil {
			viewErr = e.captureCommentSnapshotLocked(scope)
		}
		e.mu.Unlock()
		if viewErr != nil {
			view.Release()
			return Result{}, viewErr
		}
		return scope.executeDelete(b, view, inputs, canonical)
	case *parser.CreateQueryTable:
		return e.executeQueryTableLocked(scope, s, b, queryRoot, canonical, creationStage, creationDescriptor)
	case *parser.DeclareCursor:
		// Even an unlocked cursor retains physical lineage through derived/view
		// projections. Zero-mode entries capture identity, never row grants.
		if scope.queryLocks == nil {
			scope.queryLocks = &queryLockContext{IdentityOnly: true}
		}
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.Query.From == nil && s.Query.Values == nil)
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
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.From == nil && s.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		if b.rule.template {
			source = freezeViewWriteSources(scope, source, b.querySources)
		}
		if scope.protocolOrZero().bind != nil {
			return scope.installProtocolProducer(queryRoot, source, canonical)
		}
		if scope.sqlRoutine.scalar {
			rows, count, err := finalizeSQLRoutineQuery(ctx, queryRoot, source, canonical)
			return Result{Count: int64(count), Rows: rows}, err
		}
		rows, count, queryErr := exec.FinalizeSelect(ctx, queryRoot, source, canonical, op.parallelWorkers)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if b.query.RegclassOutput && count != 0 {
			if err := e.captureRegclassOutput(op, rows.(*finalRows)); err != nil {
				rows.Close()
				return Result{}, err
			}
		}
		return Result{Count: int64(count), Rows: rows}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
}
