package core

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// executeViewTriggerLocked consumes Engine.Mu. The target is a SELECT recipe,
// not a heap: only SQL reached from the admitted chain can stage physical rows.
func (e *Engine) executeViewTriggerLocked(s *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, parameters []sqltype.Value) (Result, error) {
	b := view.PhysicalBinding(execution.binding)
	// Command source preparation admits logical OLD and FROM/USING together,
	// before the existing RLS fence pins their complete physical ref union.
	// Source-free INSERT still needs the initial lazy trigger snapshot roots.
	if s.View == nil {
		if _, err := s.AcquireDataViewsLocked(e, nil); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	var root plan.Node
	var target *exec.Source
	var err error
	if _, insert := stmt.(*parser.Insert); !insert {
		prepared := execution.viewTarget
		if prepared == nil {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		root = prepared.root
		target, err = e.captureStoredSourceLocked(s, prepared, s.QueryLockContext)
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	inputs, err := e.CaptureQuerySourcesLocked(s, b.QuerySources, len(b.QuerySources) == 0)
	if err == nil && b.Returning != nil {
		err = e.CaptureCommentSnapshotLocked(s)
	}
	e.Mu.Unlock()
	if err != nil {
		if target != nil {
			target.Close()
		}
		if inputs != nil {
			inputs.Close()
		}
		return Result{}, err
	}
	outcome, err := dml.ExecuteViewTrigger(s.WriteHost(), dml.ViewTriggerCommand{
		Binding: b, TargetRoot: root, Target: target, Inputs: inputs, Parameters: parameters,
		Evaluation: execution.evaluation, QueryRoot: execution.queryRoot,
		Catalog: execution.auth.Catalog, Expressions: execution.expressions,
	})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}
