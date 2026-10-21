package engine

import (
	"fmt"

	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) executeForeignDDL(scope *StatementScope, s *parser.ForeignDDL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Kind == "table" && s.Action == "alter" {
		return e.executeForeignTableAlter(scope, s)
	}
	if s.Action == "import" {
		return e.executeForeignImport(scope, s)
	}
	op := scope.op
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	jobs, roots, err := e.stageForeignLocked(scope, auth, stage, s)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if len(roots) != 0 {
		if err := scope.stageExpressionDependents(stage, roots, s.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err := fdw.RunValidations(op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

// stageForeignLocked stages WRAPPER, SERVER, USER MAPPING and CREATE or
// DROP FOREIGN TABLE through fdw.StageDDL on the statement's ddlHost.
func (e *Engine) stageForeignLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, s *parser.ForeignDDL) ([]fdw.Validation, []ids.ObjectAddress, error) {
	return fdw.StageDDL(ddlHost{e: e, op: scope.op, stage: stage}, auth, s, scope.input)
}
