package core

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// executeInsertLocked runs INSERT VALUES and INSERT ... SELECT. It is entered
// with Engine.mu held: it captures the snapshots, partition routing and query
// sources, releases Engine.mu, selects and freezes the sources, and then the
// dml executor evaluates and stages the rows.
func (e *Engine) executeInsertLocked(scope *StatementScope, execution *preparedCommandExecution, canonical []sqltype.Value) (Result, error) {
	ctx := scope.Op.Context()
	b, auth, evaluation := view.PhysicalBinding(execution.binding), execution.auth, execution.evaluation
	if err := e.CaptureCheckCommentSnapshotLocked(scope, b.Relation); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := scope.capturePartitionInsertLocked(e, auth, b.Relation); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	h := scope.WriteHost()
	dml.BeginInsertReturning(h, b)
	keepReturning := false
	if scope.InsertReturning != nil {
		defer func() {
			if !keepReturning {
				scope.InsertReturning.Rows.Close()
			}
		}()
	}
	in := dml.InsertInput{Context: ctx, Catalog: auth.Catalog}
	if b.CommandQuery {
		source, sourceErr := e.CaptureQuerySourcesLocked(scope, b.QuerySources, len(b.QuerySources) == 0)
		if sourceErr != nil {
			e.Mu.Unlock()
			return Result{}, sourceErr
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.Mu.Unlock()
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		source = view.FreezeWriteSources(source, b.QuerySources, scope.authority, HostOf(scope))
		b.Query = &plan.Query{Root: execution.queryRoot}
		in.Query, in.Parameters, in.Schema = source, canonical, schema
	} else {
		if b.SubqueryDemand {
			inputs, sourceErr := e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
			if sourceErr != nil {
				e.Mu.Unlock()
				return Result{}, sourceErr
			}
			state := &exec.State{Source: inputs, SourceMode: exec.SourceReusable, Parameters: canonical, Input: scope.Input, Output: scope.Output, Locking: inputs.Locks != nil, Ctes: scope.cteInvocation}
			defer state.ReleaseMemberships()
			evaluation.Query = state
			evaluation.Scope, evaluation.Frame = 1, 1
		}
		e.Mu.Unlock()
		if evaluation.Query != nil {
			if err := exec.SelectQuerySources(ctx, evaluation.Query.Source); err != nil {
				return Result{}, err
			}
			evaluation.Query.Source = view.FreezeWriteSources(evaluation.Query.Source, b.QuerySources, scope.authority, HostOf(scope))
			defer evaluation.Query.Source.Close()
		}
		in.Evaluation, in.Expressions = &evaluation, execution.expressions
	}
	in.Binding = b
	outcome, err := dml.ExecuteInsert(h, in)
	if evaluation.Query != nil {
		err = evaluation.Query.FinishSources(err)
		if err != nil && outcome.Rows != nil {
			outcome.Rows.Close()
		}
	}
	if err != nil {
		return Result{}, err
	}
	keepReturning = true
	return outcomeResult(outcome), nil
}

// outcomeResult is the Result of a dml executor's outcome; Rows stays a nil
// interface without RETURNING.
func outcomeResult(outcome dml.Outcome) Result {
	result := Result{Count: outcome.Count}
	if outcome.Rows != nil {
		result.Rows = outcome.Rows
	}
	return result
}
