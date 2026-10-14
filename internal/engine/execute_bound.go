package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type preparedCommandExecution struct {
	binding     binding
	auth        AuthorizationView
	evaluation  EvalContext
	queryRoot   LogicalNode
	expressions [][]BoundExpr
	primaryTag  string
}

// Enter with Engine.mu held; every outcome releases it. Binding, signature and
// parameter import are statement-wide and have already completed exactly once.
func (e *Engine) executeBoundCommandLocked(scope *StatementScope, stmt parser.Statement, b binding, auth AuthorizationView, canonical []sqltype.Value, evaluation EvalContext, creationStage *CatalogStage, creationDescriptor *relationDescriptor, prepared *preparedCommandExecution) (Result, error) {
	op := scope.op
	ctx := op.Context()
	var err error
	expressions := b.expressions
	expressionsChanged := false
	quoteCaptures := evaluation.quoteCaptures
	_, inserting := stmt.(*parser.Insert)
	var queryRoot LogicalNode
	_, deleting := stmt.(*parser.Delete)
	if inserting || b.merge != nil || b.update != nil || b.query != nil || (deleting && (b.deleteUsing != nil || b.predicate != nil || b.returning != nil)) || len(b.defaults) != 0 {
		// Planning-owned conversions precede executor ACLs, without retaining
		// Engine.mu or acquiring a data view. Analysis/namespace checks remain
		// earlier, and only this execution's imported frame is materialized.
		e.mu.Unlock()
		if b.conflict != nil {
			if b.conflict.predicate != nil {
				b.conflict.predicate, err = normalizeConflictPredicate(&evaluation, b.conflict.predicate)
				if err != nil {
					return Result{}, err
				}
			}
			if err := b.conflict.selectArbiters(&evaluation, b.physicalBinding().relation, auth.catalog); err != nil {
				return Result{}, err
			}
			scope.insertConflict = b.conflict
			if err := b.conflict.preflightUpdate(&evaluation); err != nil {
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
				var privateRow []BoundExpr
				for j, expression := range row {
					if len(b.expressions) == 1 && len(b.identityDiscard) != 0 && b.identityDiscard[b.targets[j]] {
						continue
					}
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, insertValueError(auth.catalog, foldErr, destination.columns[destinationTargets[j]], b.insertRows[i][j], nil)
					}
					if changed {
						if privateRow == nil {
							privateRow = append([]BoundExpr(nil), row...)
						}
						privateRow[j] = folded
					}
				}
				if privateRow != nil {
					if !expressionsChanged {
						expressions = append([][]BoundExpr(nil), b.expressions...)
						expressionsChanged = true
					}
					expressions[i] = privateRow
				}
			}
		}
		if b.merge != nil {
			err = preflightMerge(&evaluation, b.merge, auth.catalog)
		} else if b.update != nil {
			b.update, err = preflightUpdate(&evaluation, b.update)
			if err == nil && b.update.from != nil {
				err = validateQueryJoinExecution(&evaluation, b.update.from, auth.catalog, 0)
			}
		} else if b.query != nil {
			queryRoot, err = preflightQuery(&evaluation, b.query.Root)
			if err == nil {
				err = validateQueryJoinExecution(&evaluation, queryRoot, auth.catalog, 0)
			}
		} else if deleting {
			if b.deleteUsing != nil {
				b.deleteUsing, err = preflightQuery(&evaluation, b.deleteUsing)
				if err == nil {
					err = validateQueryJoinExecution(&evaluation, b.deleteUsing, auth.catalog, 0)
				}
			}
			if err == nil && b.predicate != nil {
				b.predicate, _, err = preflightCoercions(&evaluation, b.predicate)
			}
		}
		if err == nil && b.returning != nil {
			b.returning, err = preflightReturning(&evaluation, b.returning)
		}
		if err == nil && b.subqueryDemand && (inserting || b.update != nil || deleting || b.merge != nil) {
			err = validateMutationJoinExecution(&evaluation, &b, expressions, auth.catalog)
		}
		if err != nil {
			return Result{}, err
		}
		if b.viewWrite != nil {
			var checks []viewCheckGroup
			for i, group := range b.viewWrite.checks {
				var predicates []BoundExpr
				for j, expression := range group.predicates {
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, foldErr
					}
					if changed {
						if predicates == nil {
							predicates = append([]BoundExpr(nil), group.predicates...)
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
		var privateDefaults []BoundExpr
		for i, expression := range b.defaults {
			if expression == nil {
				continue
			}
			defaultEvaluation := evaluation
			physical := b.physicalBinding()
			attribute := physical.relation.attributes[i]
			defaultEvaluation.quoteOrigin = quoteOrigin{owner: physical.ref, attribute: attribute, definition: physical.relation.defaults[attribute].OID}
			folded, changed, foldErr := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if foldErr != nil {
				return Result{}, foldErr
			}
			if changed {
				if privateDefaults == nil {
					privateDefaults = append([]BoundExpr(nil), b.defaults...)
				}
				privateDefaults[i] = folded
			}
		}
		if privateDefaults != nil {
			b.defaults = privateDefaults
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
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if b.viewWrite == nil && b.relation.relationKind != materializedViewRelation {
			if err := tableKind(b.relation, "change"); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Released MERGE checks read-only after analysis/planning conversions, but
	// before executor column/source ACLs. Other statements keep their ordering.
	if b.merge != nil && scope.owner.readOnly {
		e.mu.Unlock()
		tag := "MERGE"
		if prepared != nil && prepared.primaryTag != "" {
			tag = prepared.primaryTag
		}
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if err = auth.checkBinding(b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil && b.parameters.Count == 0 && quoteCaptures.private {
		scope.Expected.quotes = quoteCaptures.entries
		scope.Expected.quoteGeneration = op.session.discardPlansGeneration
	}
	quoteCaptures.sealed = true
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
				if leaf.mark.mode != 0 && leaf.relation.relationKind == viewRelation {
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
	if b.relation != nil && b.relation.relationKind == materializedViewRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			e.mu.Unlock()
			return Result{}, tableKind(b.relation, "change")
		}
	}
	if b.query != nil || b.subqueryDemand || len(b.querySources) != 0 {
		if err = e.initializeQueryLocksLocked(scope, queryRoot, b.querySources); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.visibilityDemand = scope.visibilityDemand || b.visibilitySnapshot
	if b.query != nil {
		scope.visibilityDemand = scope.visibilityDemand || b.query.visibilitySnapshot
	}
	if b.returning != nil {
		scope.visibilityDemand = scope.visibilityDemand || b.returning.visibilitySnapshot
	}
	if scope.visibilityDemand {
		err = e.captureVisibilitySnapshotLocked(scope)
	}
	if err == nil && b.relation != nil {
		err = e.captureCheckVisibilitySnapshotLocked(scope, b.relation)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.regclassConcat && b.query == nil {
		if err = e.captureCommentSnapshotLocked(scope); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := e.configureRowSecurityLocked(scope, b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	execution := preparedCommandExecution{binding: b, auth: auth, evaluation: evaluation, queryRoot: queryRoot, expressions: expressions}
	if prepared != nil {
		execution.primaryTag = prepared.primaryTag
		*prepared = execution
		e.mu.Unlock()
		return Result{}, nil
	}
	return e.executePreparedCommandLocked(scope, stmt, &execution, canonical, creationStage, creationDescriptor)
}

func (e *Engine) executePreparedCommandLocked(scope *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, canonical []sqltype.Value, creationStage *CatalogStage, creationDescriptor *relationDescriptor) (Result, error) {
	op := scope.op
	ctx := op.Context()
	var err error
	b, auth, evaluation := execution.binding, execution.auth, execution.evaluation
	queryRoot, expressions := execution.queryRoot, execution.expressions
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
		refs, err := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
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
			target.close()
			e.mu.Unlock()
			return Result{}, err
		}
		schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
		e.mu.Unlock()
		return e.executeMerge(ctx, scope, b, target, inputs, canonical, schema)
	case *parser.Update:
		err = e.captureCheckCommentSnapshotLocked(scope, b.relation)
		if err == nil && b.returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var view *VisibilityView
		if len(b.querySources) != 0 {
			refs, collectErr := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
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
		var source *querySource
		if scope.rlsTargetDenied {
			source, err = captureDeniedQuerySource(view, b.ref)
		} else {
			source, err = captureQuerySource(ctx, view, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var from *querySource
		if b.update.from != nil || b.subqueryDemand {
			from, err = e.captureQuerySourcesLocked(scope, b.querySources, false)
			if err != nil {
				source.close()
				e.mu.Unlock()
				return Result{}, err
			}
		}
		schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
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
			schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
			e.mu.Unlock()
			if err := selectQuerySources(ctx, source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil {
				source = freezeViewWriteSources(scope, source, b.querySources)
			}
			b.query = &BoundQuery{Root: queryRoot}
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
			state := &queryState{source: inputs, parameters: canonical, input: scope.input, output: scope.output, locking: inputs.locks != nil, ctes: scope.cteInvocation}
			defer state.releaseMemberships()
			evaluation.query = state
			evaluation.Scope, evaluation.Frame = 1, 1
		}
		e.mu.Unlock()
		if evaluation.query != nil {
			if err := selectQuerySources(ctx, evaluation.query.source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil {
				evaluation.query.source = freezeViewWriteSources(scope, evaluation.query.source, b.querySources)
			}
			defer evaluation.query.source.close()
		}
		var rows [][]sqltype.Value
		if scope.insertReturning == nil {
			rows = make([][]sqltype.Value, 0, len(b.insertRows))
		}
		for i, source := range b.insertRows {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			row := make([]sqltype.Value, len(b.relation.columns))
			for j, c := range b.relation.columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			if len(b.insertProjection) != 0 {
				if err = evaluateIdentityValuesRow(auth.catalog, &b, expressions[i], source, row, len(b.insertRows) > 1, &evaluation); err != nil {
					return Result{}, err
				}
				schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
				if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
					return Result{}, err
				}
				admitted, admitErr := scope.admitInsertProducer(b, row, rows, &evaluation)
				if admitErr != nil {
					return Result{}, admitErr
				}
				if admitted {
					if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
						return Result{}, err
					}
					rows = append(rows, row)
				}
				continue
			}
			if len(b.defaults) != 0 {
				supplied := b.expressions[i]
				for j, target := range b.targets {
					if j < len(supplied) && supplied[j] != nil && (len(b.arrayTargets) == 0 || b.arrayTargets[j] == nil) {
						row[target] = sqltype.Value{}
					}
				}
				for j, expression := range b.defaults {
					if expression != nil && row[j].Type() != nil {
						row[j], err = expression.Eval(&evaluation)
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
				c := b.relation.columns[target]
				var value sqltype.Value
				if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
					value, err = b.arrayTargets[j].applyExpression(&evaluation, row[target], expressions[i][j])
				} else {
					value, err = expressions[i][j].Eval(&evaluation)
				}
				if err == nil {
					row[target] = value
				}
				if err != nil {
					return Result{}, insertValueError(auth.catalog, err, c, v, nil)
				}
			}
			schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
			if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return Result{}, err
			}
			admitted, admitErr := scope.admitInsertProducer(b, row, rows, &evaluation)
			if admitErr != nil {
				return Result{}, admitErr
			}
			if admitted {
				if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
					return Result{}, err
				}
				rows = append(rows, row)
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
		var view *VisibilityView
		var viewErr error
		if len(b.querySources) != 0 {
			refs, err := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			view, viewErr = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, viewErr = scope.acquireDataViewLocked(e, b.ref)
		}
		var inputs *querySource
		if viewErr == nil && (b.deleteUsing != nil || b.subqueryDemand) {
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
			scope.queryLocks = &queryLockContext{identityOnly: true}
		}
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.Query.From == nil && s.Query.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := selectQuerySources(ctx, source); err != nil {
			source.close()
			return Result{}, err
		}
		return scope.installSQLCursor(s, queryRoot, source, canonical)
	case *parser.Select:
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.From == nil && s.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := selectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		rows, count, queryErr := finalizeSelectQuery(ctx, queryRoot, source, canonical, op.parallelWorkers)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if b.query.regclassOutput && count != 0 {
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
