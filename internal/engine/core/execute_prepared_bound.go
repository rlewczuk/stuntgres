package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

func (e *Engine) executePreparedCommandLocked(scope *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, canonical []sqltype.Value, creationStage *CatalogStage, creationDescriptor *catalog.Relation, cteOwner *exec.CTEInvocation, cteDefinition *plan.CTEDefinition) (result Result, failure error) {
	if execution.authority != nil {
		scope.authority = execution.authority
		scope.authority.Host = HostOf(scope)
	}
	var auto *autoExplainFrame
	var err error
	plSimple := scope.sqlRoutine.lexical != nil && scope.sqlRoutine.cursor == nil && !scope.sqlRoutine.plArgumentRow && !scope.sqlRoutine.plExpressionFallback && execution.binding.Query != nil && plan.PLSimpleExpression(execution.binding.Query.Root)
	if !plSimple {
		auto, err = scope.beginAutoExplainLocked(stmt)
		if err == nil {
			err = e.captureAutoExplainParametersLocked(scope, auto, canonical)
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	var observation *exec.Observation
	if owner := scope.explain; owner != nil || auto != nil {
		previous := scope.invocationContext
		timing := auto != nil && auto.options.Timing
		if owner != nil {
			timing = timing || owner.options.Timing
		}
		if auto != nil && auto.observation != nil {
			observation = auto.observation
			scope.invocationContext = observation.Context(scope.executionContext())
		} else if owner == nil && !auto.options.Analyze {
			scope.invocationContext, observation = exec.ObservePresentation(scope.executionContext())
		} else {
			scope.invocationContext, observation = exec.Observe(scope.executionContext(), timing)
		}
		options := catalog.ForeignExplainOptions{}
		structured := false
		if auto != nil {
			options.Costs = true
			options.Verbose, structured = auto.options.Verbose, auto.options.Structured()
			auto.observation = observation
			observation.SetLogicalMetrics(auto.buffers, auto.wal, auto.triggers)
		}
		if owner != nil {
			options.Verbose = options.Verbose || owner.options.Verbose
			options.Costs = options.Costs || owner.options.Costs
			structured = structured || owner.options.Structured()
		}
		observation.SetForeignOptions(options, structured)
		dml := reserveExecutedDML(observation, cteOwner, cteDefinition, stmt, execution.binding)
		scope.invocationContext = dml.Context(scope.executionContext())
		defer func() { scope.invocationContext = previous }()
		evaluationContext := execution.evaluation.Context
		previousEvaluationContext := evaluationContext
		defer func() { execution.evaluation.Context = previousEvaluationContext }()
		if evaluationContext == nil {
			evaluationContext = scope.executionContext()
		}
		execution.evaluation.Context = observation.Context(evaluationContext)
	} else if scope.statementStatsDepth != 0 && exec.HasObservation(scope.executionContext()) {
		previous, evaluation := scope.invocationContext, execution.evaluation.Context
		scope.invocationContext = exec.WithoutObservation(scope.executionContext())
		execution.evaluation.Context = scope.executionContext()
		defer func() { scope.invocationContext, execution.evaluation.Context = previous, evaluation }()
	}
	actualTree, captureErr := e.captureExplainCommandLocked(scope, stmt, execution, observation, cteOwner, cteDefinition)
	if captureErr != nil {
		e.Mu.Unlock()
		return Result{}, captureErr
	}
	var autoTree *explain.Tree
	if auto != nil {
		renderer, tree, err := e.captureExecutedPlanLocked(scope, stmt, execution, observation, auto.options, cteOwner, cteDefinition)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		renderer.Commands = &auto.renderers
		renderer.SuppressActual = !auto.options.Analyze
		auto.renderers = append(auto.renderers, renderer)
		if stmt == auto.primary && len(auto.renderers) > 1 {
			copy(auto.renderers[1:], auto.renderers[:len(auto.renderers)-1])
			auto.renderers[0] = renderer
		}
		autoTree = tree
		auto.resume()
		autoStarted := auto.started
		sharedRoot := actualTree != nil && autoTree != nil && actualTree.Actual == autoTree.Actual
		defer func() {
			auto.pause()
			auto.failed = auto.failed || failure != nil
			if autoTree != nil && autoTree.Actual != nil && !sharedRoot && failure == nil {
				if auto.options.Timing {
					autoTree.Actual.Elapsed = time.Since(autoStarted)
				}
				if rows, ok := result.Rows.(*exec.FinalRows); ok {
					autoTree.Actual.Rows = int64(rows.Count)
				} else if _, selectStatement := stmt.(*parser.Select); selectStatement {
					autoTree.Actual = nil
				}
			}
		}()
	}
	var actualStart time.Time
	if actualTree != nil && (scope.explain.options.Timing || auto != nil && auto.options.Timing) {
		actualStart = time.Now()
	}
	if actualTree != nil {
		defer func() {
			if failure == nil {
				actual := actualTree.Actual
				if rows, ok := result.Rows.(*exec.FinalRows); ok {
					actual.Rows = int64(rows.Count)
				} else if _, selectStatement := stmt.(*parser.Select); selectStatement {
					// Streaming nested commands retain their genuine stage
					// frontiers until the existing owner consumes/closes them.
					actualTree.Actual = nil
				}
				if !actualStart.IsZero() {
					actual.Elapsed = time.Since(actualStart)
				}
			}
		}()
	}
	// Preparation provenance normally ends at this shared executor handoff,
	// including expressions reached from EXCEPTION handlers. Imported-parameter
	// computations reached during preflight record their own execution origin.
	scope.sqlRoutine.plPlanning = false
	// PL preparation retains its expression traceback. Once a checked simple
	// scalar enters execution, PostgreSQL's simple-expression route has no SPI
	// query callback; complex query expressions retain that callback. Record
	// this actual boundary only on failure, without changing error identity.
	plExpression := scope.sqlRoutine.lexical != nil && scope.sqlRoutine.cursor == nil
	simplePL := plExpression && !scope.sqlRoutine.plArgumentRow && !scope.sqlRoutine.plExpressionFallback && execution.binding.Query != nil && plan.PLSimpleExpression(execution.binding.Query.Root)
	scope.sqlRoutine.plSimpleExpression = simplePL
	defer func() {
		if failure != nil && plExpression {
			failure = &proc.PLExpressionExecutionError{Err: failure, Simple: simplePL}
		}
	}()
	previousRLSCommand := scope.rlsCommand
	scope.rlsCommand = execution
	defer func() { scope.rlsCommand = previousRLSCommand }()
	op := scope.Op
	ctx := scope.executionContext()
	b, auth := view.PhysicalBinding(execution.binding), execution.auth
	scope.installExportDemandFor(&b, stmt)
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge, *parser.Call:
		// RC enters here after target admission without snapshotPhase. Pin
		// this actual command's targets even when ordinary mutation scratch
		// has no Statement (unlike CTE/rule commands).
		previous := scope.snapshotTargetAdmission
		scope.snapshotTargetAdmission = session.SnapshotTargetAdmission(stmt)
		err := e.captureStatementSnapshotLocked(scope)
		scope.snapshotTargetAdmission = previous
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	queryRoot := execution.queryRoot
	if scope.protocolOrZero().simple && !scope.protocolOrZero().simpleCreated {
		e.Mu.Unlock()
		if err := scope.beginProtocolSimplePortal(); err != nil {
			return Result{}, err
		}
		e.Mu.Lock()
	}
	if err := e.materializePolicySourcesLocked(scope); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if b.ViewWrite != nil && view.ViewWriteOf(b.ViewWrite).Instead {
		return e.executeViewTriggerLocked(scope, stmt, execution, canonical)
	}
	if b.Relation != nil && b.Relation.RelationKind == catalog.ForeignRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			return e.executeForeignCommandLocked(scope, stmt, execution, canonical)
		}
	}
	switch s := stmt.(type) {
	case *parser.Call:
		e.Mu.Unlock()
		evaluationContext := scope.ActivateRuntime(ctx)
		evaluation := execution.evaluation
		evaluation.Context = evaluationContext
		value, err := execution.expressions[0][0].Eval(&evaluation)
		if err != nil {
			return Result{}, err
		}
		schema := analyze.RoutineCallSchema(b)
		if len(schema) == 0 {
			return Result{CommandTag: "CALL"}, nil
		}
		if value.IsNull() {
			return Result{}, SQLError("XX000", "procedure returned null record")
		}
		fields := make([]sqltype.Value, len(schema))
		for i := range fields {
			fields[i] = value.CompositeField(i)
		}
		result, err := anonymousResult(schema, [][]sqltype.Value{fields}, 0)
		result.CommandTag = "CALL"
		return result, err
	case *parser.Merge:
		err := e.CaptureCheckCommentSnapshotLocked(scope, b.Relation)
		if err == nil && b.Returning != nil {
			err = e.CaptureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		collectAuth := auth
		collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
		refs, err := e.QueryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, []ids.ObjectRef{b.Ref}, scope.Op.Session.SessionToken, plan.RowMark{})
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		dataView, err := scope.AcquireDataViewsLocked(e, refs)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		var target *exec.Source
		if scope.RLS.TargetDenied {
			target, err = view.CaptureDeniedQuerySource(dataView, b.Ref)
		} else {
			target, err = exec.CaptureSource(ctx, dataView, b.Ref)
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		e.initializeTemporarySourceLocked(scope, target)
		inputs, err := e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
		if err != nil {
			target.Close()
			e.Mu.Unlock()
			return Result{}, err
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.Mu.Unlock()
		ctx = scope.ActivateRuntime(ctx)
		return executeMerge(ctx, scope, b, target, inputs, canonical, schema)
	case *parser.Update:
		if err = scope.capturePartitionInsertLocked(e, auth, b.Relation); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		err = e.CaptureCheckCommentSnapshotLocked(scope, b.Relation)
		for _, target := range scope.InheritedTargets {
			if err != nil {
				break
			}
			err = e.CaptureCheckCommentSnapshotLocked(scope, target.Relation)
		}
		if err == nil && b.Returning != nil {
			err = e.CaptureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		var view *visibility.View
		if len(b.QuerySources) != 0 || len(scope.InheritedTargets) != 0 || b.Relation.Partition != nil {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, collectErr := e.QueryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, scope.Command.TargetRefs(b.Ref), scope.Op.Session.SessionToken, plan.RowMark{})
			if collectErr != nil {
				e.Mu.Unlock()
				return Result{}, collectErr
			}
			view, err = scope.AcquireDataViewsLocked(e, refs)
		} else {
			view, err = scope.acquireDataViewLocked(e, b.Ref)
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		source, err := scope.Command.CaptureSource(ctx, view, b)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		borrowQueryAuthority(source, scope)
		e.initializeTemporarySourceLocked(scope, source)
		var from *exec.Source
		if b.Update.From != nil || b.SubqueryDemand {
			from, err = e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
			if err != nil {
				source.Close()
				e.Mu.Unlock()
				return Result{}, err
			}
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.Mu.Unlock()
		ctx = scope.ActivateRuntime(ctx)
		return executeUpdate(ctx, scope, b, source, from, canonical, schema, execution.evaluation.DefaultOccurrence)
	case *parser.Insert:
		return e.executeInsertLocked(scope, execution, canonical)
	case *parser.Delete:
		var view *visibility.View
		var viewErr error
		if len(b.QuerySources) != 0 || len(scope.InheritedTargets) != 0 {
			collectAuth := auth
			collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
			refs, err := e.QueryTableRefsLocked(collectAuth, auth.State.CurrentUser, b.QuerySources, scope.Command.TargetRefs(b.Ref), scope.Op.Session.SessionToken, plan.RowMark{})
			if err != nil {
				e.Mu.Unlock()
				return Result{}, err
			}
			view, viewErr = scope.AcquireDataViewsLocked(e, refs)
		} else {
			view, viewErr = scope.acquireDataViewLocked(e, b.Ref)
		}
		var inputs *exec.Source
		if viewErr == nil && (b.Delete.Using != nil || b.SubqueryDemand) {
			inputs, viewErr = e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
		} else if viewErr == nil && b.Returning != nil {
			viewErr = e.CaptureCommentSnapshotLocked(scope)
		}
		e.Mu.Unlock()
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
		if scope.QueryLockContext == nil {
			scope.QueryLockContext = &exec.LockContext{IdentityOnly: true}
		}
		source, sourceErr := e.captureSelectSourcesLocked(scope, queryRoot, b.QuerySources, canonical, s.Query.From == nil && s.Query.Values == nil)
		e.Mu.Unlock()
		ctx = scope.ActivateRuntime(ctx)
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			source.Close()
			return Result{}, err
		}
		return scope.installSQLCursor(s, queryRoot, source, canonical)
	case *parser.Select:
		if scope.sqlRoutine.cursor != nil && scope.QueryLockContext == nil {
			scope.QueryLockContext = &exec.LockContext{IdentityOnly: true}
		}
		source, sourceErr := e.captureSelectSourcesLocked(scope, queryRoot, b.QuerySources, canonical, s.From == nil && s.Values == nil)
		e.Mu.Unlock()
		ctx = scope.ActivateRuntime(ctx)
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			source.Close()
			return Result{}, err
		}
		if cursor := scope.sqlRoutine.cursor; cursor != nil {
			scroll := cursor.Scroll == parser.CursorScrollEnabled
			if source.Locks.RowMarked() {
				if scroll {
					source.Close()
					err := SQLError("0A000", "DECLARE SCROLL CURSOR ... FOR UPDATE/SHARE is not supported").(*sqlstate.Error)
					err.Detail = "Scrollable cursors must be READ ONLY."
					return Result{}, err
				}
			} else if cursor.Scroll == parser.CursorScrollDefault {
				scroll = exec.SupportsBackwardCursor(queryRoot, source)
			}
			installed, name, err := scope.installCursor(cursor, scope.sqlRoutine.cursorName, queryRoot, source, canonical, scroll)
			scope.sqlRoutine.openedCursor, scope.sqlRoutine.openedCursorName = installed, name
			return Result{}, err
		}
		source = view.FreezeWriteSources(source, b.QuerySources, scope.authority, HostOf(scope))
		if scope.protocolOrZero().bind != nil && scope.explain == nil {
			bind := scope.protocolOrZero().bind
			bind.autoExplainBinding = execution.binding
			bind.autoExplainBinding.Expressions = execution.expressions
			bind.autoExplainStatement = stmt
			return scope.installProtocolProducer(queryRoot, source, canonical)
		}
		if scope.sqlRoutine.plCapture || scope.sqlRoutine.nestedCursor || scope.sqlRoutine.rows && !parser.HasModifyingCTE(stmt) {
			rows, err := exec.OpenNestedQueryRows(ctx, queryRoot, source, canonical)
			return Result{Rows: rows}, err
		}
		if scope.sqlRoutine.scalar || scope.sqlRoutine.plArgumentRow {
			scalar := !scope.sqlRoutine.plArgumentRow && (scope.sqlRoutine.result.Type == nil || scope.sqlRoutine.result.Type.ID() != 2249 && !scope.sqlRoutine.result.Type.BaseType().IsComposite())
			rows, count, err := finalizeSQLRoutineQuery(ctx, queryRoot, source, canonical, scalar)
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
	e.Mu.Unlock()
	return Result{}, SQLError("0A000", "unsupported statement")
}
